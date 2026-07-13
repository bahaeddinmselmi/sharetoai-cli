package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

func runLogin() error {
	fmt.Println("Generate a CLI API key at https://sharetoai.app/account (\"CLI API key\" section),")
	fmt.Print("then paste it here: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return err
	}
	key := strings.TrimSpace(line)
	if key == "" {
		return errors.New("no key entered")
	}

	if err := saveApiKey(key); err != nil {
		return fmt.Errorf("could not save key: %w", err)
	}

	path, _ := credentialsPath()
	fmt.Printf("Saved to %s\n", path)
	return nil
}
