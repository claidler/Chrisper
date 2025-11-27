package main

import (
"bufio"
"fmt"
"log"
"os"
"chrisper/pkg/dictation"
)

func main() {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY is not set")
	}

	s, err := dictation.New(apiKey)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	s.OnStart = func() { fmt.Println("Recording started...") }
	s.OnStop = func() { fmt.Println("Recording stopped...") }
	s.OnProcessing = func() { fmt.Println("Processing...") }
	s.OnError = func(err error) { fmt.Printf("Error: %v\n", err) }

	fmt.Println("Press Enter to toggle recording. Ctrl+C to exit.")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		s.ToggleRecording()
	}
}
