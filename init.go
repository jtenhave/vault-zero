package vaultzero

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	vzaws "github.com/jtenhave/vaultzero/aws"
)

const manifestFileName = "manifest.json"
const globalMetadataFileName = "global_metadata.json"

type GlobalMetadataFileData struct {
	MachineID string `json:"machine_id"`
}

type ArchiveFileData struct {
	ID string `json:"id"`
}

type DataDirFileData struct {
	ID              string `json:"id"`
	BucketName      string `json:"bucket_name"`
	AccessKey       string `json:"access_key"`
	SecretAccessKey string `json:"secret_access_key"`
}

func Init(ctx context.Context, args []string) (err error) {
	rollbacks := []func() error{}
	defer func() {
		if err != nil {
			for _, rollback := range rollbacks {
				rollback()
			}
		}
	}()

	if len(args) < 1 {
		fmt.Println("⚠️ Usage: vault-zero init <root folder>")
		return nil
	}

	fmt.Println("Starting vault-zero archive initialization...")

	var isValid bool
	rootFolder, isValid, err := validateRootFolder(args[0])
	if !isValid {
		return err
	}

	var bucketName string
	var accessKey string
	var secretAccessKey string

	for {
		accessKey, isValid = promptForAccessKey()
		if !isValid {
			continue
		}

		secretAccessKey, isValid = promptForSecretAccessKey()
		if !isValid {
			continue
		}

		userId, isValid, err := validateAWSCredentials(ctx, accessKey, secretAccessKey)
		if err != nil {
			return err
		}

		if !isValid {
			continue
		}

		bucketName = fmt.Sprintf("vz-archive-%s", userId)

		break
	}

	archiveID, dataDirRollbackFunc, err := setupDataDir(rootFolder, bucketName, accessKey, secretAccessKey)
	if err != nil {
		return err
	}

	rollbacks = append(rollbacks, dataDirRollbackFunc)

	rollbackFunc, isValid, err := validateS3Bucket(ctx, archiveID, bucketName, accessKey, secretAccessKey)
	if !isValid {
		return err
	}

	if err != nil {
		return err
	}

	rollbacks = append(rollbacks, rollbackFunc)

	archiveFileRollbackFunc, err := createArchiveFile(rootFolder, archiveID)
	if err != nil {
		return err
	}

	rollbacks = append(rollbacks, archiveFileRollbackFunc)

	fmt.Println("✅ Vault-zero successfully initialized for root folder: ", rootFolder)

	return nil
}

func validateRootFolder(rootFolder string) (string, bool, error) {
	fmt.Printf("Checking root folder: %s...\n", rootFolder)

	info, err := os.Stat(rootFolder)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("❌ Root folder does not exist")
			return "", false, nil
		}

		return "", false, fmt.Errorf("cannot stat root folder: %v", err)
	}

	if !info.IsDir() {
		fmt.Println("❌ Root folder is not a directory")
		return "", false, nil
	}

	trimmedRootFolder := strings.Trim(strings.Trim(rootFolder, "\\"), "/")

	archiveFile := getArchiveFilePath(trimmedRootFolder)
	_, err = os.Stat(archiveFile)
	if err == nil {
		fmt.Println("❌ Root folder already initialized as a vault-zero archive")
		return "", false, nil
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("cannot stat archive file: %s", archiveFile)
	}

	fmt.Println("✅ Root folder is valid.")

	return trimmedRootFolder, true, nil
}

func getArchiveFilePath(rootFolder string) string {
	return fmt.Sprintf("%s\\%s", rootFolder, archiveFileName)
}

func promptForAccessKey() (string, bool) {
	fmt.Print("Enter your AWS access key: ")
	accessKey := ""
	fmt.Scanln(&accessKey)

	if accessKey == "" {
		fmt.Println("❌ Access key is required")
		return "", false
	}

	return accessKey, true
}

func promptForSecretAccessKey() (string, bool) {
	fmt.Print("Enter your AWS secret access key: ")
	secretAccessKey := ""
	fmt.Scanln(&secretAccessKey)

	if secretAccessKey == "" {
		fmt.Println("❌ Secret access key is required")
		return "", false
	}

	return secretAccessKey, true
}

func validateAWSCredentials(ctx context.Context, accessKey string, secretAccessKey string) (string, bool, error) {
	fmt.Println("Validating AWS credentials...")

	stsClient, err := vzaws.NewSTSClient(ctx, accessKey, secretAccessKey)
	if err != nil {
		return "", false, fmt.Errorf("failed to create STS client: %w", err)
	}

	callerIdentity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "InvalidClientTokenId",
				"UnrecognizedClientException",
				"InvalidAccessKeyId",
				"SignatureDoesNotMatch":
				fmt.Println("❌ Invalid AWS credentials")
				return "", false, nil

			default:
				return "", false, fmt.Errorf("AWS API error %s: %w", apiErr.ErrorCode(), err)
			}
		}
	}

	userName := strings.Split(*callerIdentity.Arn, "/")[1]
	userNameParts := strings.Split(userName, "-")

	if len(userNameParts) < 3 || userNameParts[0] != "vz" || userNameParts[1] != "user" || !isInt(userNameParts[2]) {
		fmt.Println("❌ Invalid AWS user found")
		return "", false, nil
	}

	fmt.Println("✅ AWS credentials validated.")

	return userNameParts[2], true, nil
}

func setupDataDir(rootFolder string, bucketName string, accessKey string, secretAccessKey string) (string, func() error, error) {
	fmt.Println("Setting up vault-zero data directory...")

	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return "", nil, fmt.Errorf("failed to get user config dir: %s", err)
	}

	dataDirPath := fmt.Sprintf("%s\\%s", userConfigDir, dataDir)

	_, err = os.Stat(dataDirPath)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(dataDirPath, 0755)
			if err != nil {
				return "", nil, fmt.Errorf("failed to create data dir: %s", err)
			}
		} else {
			return "", nil, fmt.Errorf("cannot stat data dir: %s", dataDirPath)
		}
	}

	machineID := ""
	globalMetadataFilePath := fmt.Sprintf("%s\\%s", dataDirPath, globalMetadataFileName)

	globalMetadataFileRaw, err := os.ReadFile(globalMetadataFilePath)
	globalMetadataFileData := GlobalMetadataFileData{}
	if err != nil {
		if os.IsNotExist(err) {
			machineIDGuid, err := uuid.NewRandom()
			if err != nil {
				return "", nil, fmt.Errorf("failed to generate machine ID: %s", err)
			}

			machineID = machineIDGuid.String()
			globalMetadataFileData.MachineID = machineID

			globalMetadataFileRaw, err := json.Marshal(globalMetadataFileData)
			if err != nil {
				return "", nil, fmt.Errorf("error marshaling metadata file to JSON: %s", err)
			}

			err = writeLocalFile(globalMetadataFilePath, globalMetadataFileRaw)
			if err != nil {
				return "", nil, fmt.Errorf("failed to write global metadata file: %s", err)
			}
		} else {
			return "", nil, fmt.Errorf("cannot read global metadata file: %s", globalMetadataFilePath)
		}
	} else {
		err = json.Unmarshal(globalMetadataFileRaw, &globalMetadataFileData)
		if err != nil {
			return "", nil, fmt.Errorf("failed to unmarshal global metadata file: %s", err)
		}

		machineID = globalMetadataFileData.MachineID
	}

	archiveIDHash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s", rootFolder, accessKey, machineID)))
	archiveID := hex.EncodeToString(archiveIDHash[:8])

	dataDirFileData := DataDirFileData{
		ID:              archiveID,
		BucketName:      bucketName,
		AccessKey:       accessKey,
		SecretAccessKey: secretAccessKey,
	}

	dataDirFileDataBytes, err := json.Marshal(dataDirFileData)
	if err != nil {
		return "", nil, fmt.Errorf("error marshaling data dir file to JSON: %s", err)
	}

	dataDirFilePath := fmt.Sprintf("%s\\%s.json", dataDirPath, archiveID)
	err = writeLocalFile(dataDirFilePath, dataDirFileDataBytes)
	if err != nil {
		return "", nil, fmt.Errorf("failed to write data dir file: %s", err)
	}

	fmt.Println("✅ Data directory setup complete.")

	return archiveID, func() error {
		err := os.Remove(dataDirFilePath)
		if err != nil {
			return fmt.Errorf("failed to delete data dir file: %s", err)
		}

		return nil
	}, nil
}

func validateS3Bucket(ctx context.Context, archiveID string, bucketName string, accessKey string, secretAccessKey string) (func() error, bool, error) {
	fmt.Println("Connecting to cloud storage...")

	client, err := vzaws.NewS3Client(ctx, accessKey, secretAccessKey)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create S3 client: %w", err)
	}

	accessKeyHash := sha256.Sum256([]byte(accessKey))
	accessKeyHashString := hex.EncodeToString(accessKeyHash[:8])
	accessKeyRegisterKey := fmt.Sprintf("keys/%s", accessKeyHashString)

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(accessKeyRegisterKey),
		Body:        bytes.NewReader([]byte{}),
		IfNoneMatch: aws.String("*"),
	})

	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "PreconditionFailed", "ConditionalRequestConflict":
				fmt.Println("❌ AWS key already in use for this user. Please try again with a different key.")
				return nil, false, nil
			default:
				return nil, false, fmt.Errorf("failed to register key: %w", err)
			}
		}
	}

	rollbacks := []func() error{
		func() error {
			_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(bucketName),
				Key:    aws.String(accessKeyRegisterKey),
			})
			return err
		},
	}

	rollbackFunc := func() error {
		var err error
		for _, rollback := range rollbacks {
			err = rollback()
		}
		return err
	}

	objects, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucketName),
		Prefix:  aws.String(fmt.Sprintf("%s/", archiveID)),
		MaxKeys: aws.Int32(1),
	})

	if err != nil {
		return nil, false, fmt.Errorf("failed to list objects in bucket: %w", err)
	}

	if len(objects.Contents) > 0 {
		return nil, false, fmt.Errorf("bucket is not empty")
	}

	manifest := Manifest{}
	manifestFileData, err := json.Marshal(manifest)
	manifestRegisterKey := fmt.Sprintf("%s/%s", archiveID, manifestFileName)

	if err != nil {
		return rollbackFunc, false, fmt.Errorf("failed to marshal manifest file to JSON: %w", err)
	}

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(manifestRegisterKey),
		Body:        bytes.NewReader(manifestFileData),
		IfNoneMatch: aws.String("*"),
	})

	if err != nil {
		return rollbackFunc, false, fmt.Errorf("failed to upload manifest file to bucket: %w", err)
	}

	rollbacks = append(rollbacks, func() error {
		_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(manifestRegisterKey),
		})
		return err
	})

	fmt.Println("✅ Successfully connected to cloud storage.")

	return rollbackFunc, true, nil
}

func createArchiveFile(rootFolder string, archiveID string) (func() error, error) {
	fmt.Println("Intializing local folder as archive...")

	archiveFilePath := getArchiveFilePath(rootFolder)

	archiveFileData := ArchiveFileData{
		ID: archiveID,
	}

	archiveFileDataBytes, err := json.Marshal(archiveFileData)
	if err != nil {
		return nil, fmt.Errorf("error marshaling metadata file to JSON: %s", err)
	}

	err = writeLocalFile(archiveFilePath, archiveFileDataBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to write archive file: %s", err)
	}

	fmt.Println("✅ Successfully initialized local folder as archive.")

	return func() error {
		err := os.Remove(archiveFilePath)
		if err != nil {
			return fmt.Errorf("failed to delete archive file: %s", err)
		}

		return nil
	}, nil
}

func writeLocalFile(filepath string, data []byte) error {
	file, err := os.OpenFile(filepath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("file already exists")
		} else {
			return fmt.Errorf("failed to create file: %w", err)
		}
	}

	defer file.Close()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func isInt(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}
