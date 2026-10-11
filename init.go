package vaultzero

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	vzaws "github.com/jtenhave/vaultzero/aws"
)

const manifestFileName = "manifest.json"

type ArchiveFileData struct {
	ID string `json:"id"`
}

func Init(ctx context.Context, args []string) error {
	if len(args) < 1 {
		fmt.Println("⚠️ Usage: vault-zero init <root folder>")
		return nil
	}

	rootFolder, isValid, err := validateRootFolder(args[0])
	if !isValid {
		return err
	}

	_, isValid, err = validateNoArchiveFile(rootFolder)
	if !isValid {
		return err
	}

	fmt.Println("✅ Root folder validated.")

	_, err = ensureConfigDir()
	if err != nil {
		return err
	}

	userId := ""
	for {
		accessKey, isValid := promptForAccessKey()
		if !isValid {
			continue
		}

		secretAccessKey, isValid := promptForSecretAccessKey()
		if !isValid {
			continue
		}

		userId, isValid, err = validateAWSCredentials(ctx, accessKey, secretAccessKey)
		if err != nil {
			return err
		}

		if !isValid {
			continue
		}

		break
	}

	fmt.Println("✅ AWS credentials validated.")

	fmt.Println("User ID: ", userId)

	fmt.Println("Initializing vault-zero in: ", rootFolder)
	rollbacks := []func() error{}

	defer func() {
		if err != nil {
			for _, rollback := range rollbacks {
				rollback()
			}
		}
	}()

	return nil
}

func validateRootFolder(rootFolder string) (string, bool, error) {
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
	return trimmedRootFolder, true, nil
}

func validateNoArchiveFile(rootFolder string) (string, bool, error) {
	archiveFile := fmt.Sprintf("%s\\%s", rootFolder, archiveFileName)
	_, err := os.Stat(archiveFile)
	if err == nil {
		fmt.Println("❌ Root folder already initialized")
		return "", false, nil
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("cannot stat archive file: %s", archiveFile)
	}

	return archiveFile, true, nil
}

func ensureConfigDir() (string, error) {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %s", err)
	}

	configDirPath := fmt.Sprintf("%s\\%s", userConfigDir, configDir)
	_, err = os.Stat(configDirPath)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(configDirPath, 0755)
			if err != nil {
				return "", fmt.Errorf("failed to create config dir: %s", err)
			}
		} else {
			return "", fmt.Errorf("cannot stat config dir: %s", configDirPath)
		}
	}

	return configDirPath, nil
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

	return userNameParts[2], true, nil
}

/*func (c *initCommand) createArchiveFile(archiveFile string, rootFolder string) (func() error, error) {

	uniqueMachineID, err := getUniqueMachineID()
	if err != nil {
		return nil, fmt.Errorf("failed to get unique machine ID: %s", err)
	}

	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s", c.config.AccessKey, rootFolder, uniqueMachineID)))
	id := hex.EncodeToString(hash[:])

	archiveFileData := ArchiveFileData{
		ID: id,
	}

	archiveFileDataBytes, err := json.Marshal(archiveFileData)
	if err != nil {
		return nil, fmt.Errorf("error marshaling metadata file to JSON: %s", err)
	}

	err = os.WriteFile(archiveFile, archiveFileDataBytes, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write metadata file to root folder: %s", err)
	}

	return func() error {
		return deleteArchiveFile(archiveFile)
	}, nil
}*/

/*func deleteArchiveFile(archiveFile string) error {
	err := os.Remove(archiveFile)
	if err != nil {
		return fmt.Errorf("failed to delete metadata file: %s", err)
	}

	return nil
}*/

/*func (c *initCommand) checkBucketEmpty(ctx context.Context) error {
	objects, err := c.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(c.config.BucketName),
	})

	if err != nil {
		return fmt.Errorf("failed to list objects in bucket: %s", err)
	}

	if len(objects.Contents) > 0 {
		return fmt.Errorf("bucket is not empty")
	}

	return nil
}

func (c *initCommand) uploadManifestFile(ctx context.Context) (func() error, error) {
	manifest := Manifest{}

	manifestFileData, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal manifest file to JSON: %s", err)
	}

	_, err = c.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.config.BucketName),
		Key:         aws.String(manifestFileName),
		Body:        bytes.NewReader(manifestFileData),
		IfNoneMatch: aws.String("*"),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to upload manifest file to bucket: %s", err)
	}

	return func() error {
		return c.deleteManifestFile(ctx)
	}, nil
}

func (c *initCommand) deleteManifestFile(ctx context.Context) error {
	_, err := c.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.config.BucketName),
		Key:    aws.String(manifestFileName),
	})

	if err != nil {
		return fmt.Errorf("failed to delete manifest file from bucket: %s", err)
	}

	return nil
}

func getUniqueMachineID() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("failed to get hostname: %s", err)
	}
	return hostname, nil
}*/

func isInt(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}
