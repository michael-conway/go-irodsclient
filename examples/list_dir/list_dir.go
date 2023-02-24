package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cyverse/go-irodsclient/fs"
	"github.com/cyverse/go-irodsclient/irods/types"

	log "github.com/sirupsen/logrus"
)

func main() {
	logger := log.WithFields(log.Fields{
		"package":  "main",
		"function": "main",
	})

	// Parse cli parameters
	flag.Parse()
	args := flag.Args()

	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "Give an iRODS path!\n")
		os.Exit(1)
	}

	inputPath := args[0]

	// Read account configuration from YAML file
	yaml, err := os.ReadFile("account.yml")
	if err != nil {
		logger.Error(err)
		panic(err)
	}

	account, err := types.CreateIRODSAccountFromYAML(yaml)
	if err != nil {
		logger.Error(err)
		panic(err)
	}

	logger.Debugf("Account : %v", account.MaskSensitiveData())

	// Create a file system
	appName := "list_dir"
	filesystem, err := fs.NewFileSystemWithDefault(account, appName)
	if err != nil {
		logger.Error(err)
		panic(err)
	}

	defer filesystem.Release()

	entries, err := filesystem.List(inputPath)
	if err != nil {
		logger.Error(err)
		panic(err)
	}

	if len(entries) == 0 {
		fmt.Printf("Found no entries in the directory - %s\n", inputPath)
	} else {
		fmt.Printf("DIR: %s\n", inputPath)
		for _, entry := range entries {
			if entry.Type == fs.FileEntry {
				fmt.Printf("> FILE:\t%d\t%s\t%d\n", entry.ID, entry.Path, entry.Size)
			} else {
				// dir
				fmt.Printf("> DIRECTORY:\t%d\t%s\n", entry.ID, entry.Path)
			}

		}
	}
}
