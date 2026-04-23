package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
)

func SetAPIConfig() (string, string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("Error getting home directory: ", err)
		os.Exit(1)
	}

	envPathHomeDir := filepath.Join(homeDir, ".config/notioncli/.env")
	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Println("Error getting current directory: ", err)
		os.Exit(1)
	}

	envPathWorkingDir := filepath.Join(workingDir, ".env")
	err = godotenv.Load(envPathWorkingDir)

	if err != nil {
		// If the env file is not found in the working directory, try to load it from the home directory
		err = godotenv.Load(envPathHomeDir)
		if err != nil {
			fmt.Println("Error loading .env file: ", err)
			os.Exit(1)
		}
	}

	notionAPIKey, ok := os.LookupEnv("NOTION_API_KEY")
	if !ok {
		fmt.Println("NOTION_API_KEY environment variable not found")
		os.Exit(1)
	}
	// NOTION_PAGE_ID is no longer required at config-load time: the
	// persistent --page flag on rootCmd can supply the target page via an
	// alias or literal id. Callers that still want the env-var fallback
	// should use the resolvePageID helper in cmd/root.go — it tries
	// --page first, then NOTION_PAGE_ID, and only then errors out. Here
	// we simply return whatever happens to be in the environment
	// (possibly empty) so tests and non-page-scoped commands keep working.
	pageID := os.Getenv("NOTION_PAGE_ID")
	return notionAPIKey, pageID
}

func GetLocalTimeZone() (*time.Location, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("Error getting home directory: ", err)
		os.Exit(1)
	}

	envPathHomeDir := filepath.Join(homeDir, ".config/notioncli/.env")
	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Println("Error getting current directory: ", err)
		os.Exit(1)
	}

	envPathWorkingDir := filepath.Join(workingDir, ".env")
	err = godotenv.Load(envPathWorkingDir)

	if err != nil {
		// If the env file is not found in the working directory, try to load it from the home directory
		err = godotenv.Load(envPathHomeDir)
		if err != nil {
			fmt.Println("Error loading .env file: ", err)
			os.Exit(1)
		}
	}
	localTimeZone, ok := os.LookupEnv("LOCAL_TIMEZONE")
	if !ok {
		return nil, fmt.Errorf("LOCAL_TIMEZONE environment variable not found")
	}
	location, err := time.LoadLocation(localTimeZone)
	if err != nil {
		return nil, err
	}
	return location, nil
}
