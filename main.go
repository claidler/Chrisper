package main

import (
	"fmt"
	"log"
	"os"

	"chrisper/pkg/dictation"

	"github.com/getlantern/systray"
	hook "github.com/robotn/gohook"
)

var (
	service *dictation.Service
	// embeddedAPIKey can be set via -ldflags "-X main.embeddedAPIKey=..."
	embeddedAPIKey string
)

func main() {
	// Setup logging to file for debugging app bundle launch
	logFile := "/tmp/chrisper.log"
	f, err := os.OpenFile(logFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		// If we can't open log file, we can't do much.
	} else {
		log.SetOutput(f)
		defer f.Close()
	}
	log.Println("Chrisper Application Started")
	log.Printf("Environment: %v\n", os.Environ())

	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(iconIdle)
	systray.SetTitle("")
	systray.SetTooltip("Real-time Dictation")

	mQuit := systray.AddMenuItem("Quit", "Quit the application")

	// 1. Initialize Dictation Service
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		if embeddedAPIKey != "" {
			apiKey = embeddedAPIKey
			log.Printf("Using embedded API Key\n")
		} else {
			log.Fatal("Please set GEMINI_API_KEY environment variable.")
		}
	}

	var err error
	service, err = dictation.New(apiKey)
	if err != nil {
		log.Fatalf("Failed to initialize dictation service: %v", err)
	}
	
	// Setup Callbacks
	service.OnStart = func() {
		fmt.Println("Recording Started")
		systray.SetTitle("")
		systray.SetIcon(iconRecording)
	}
	service.OnStop = func() {
		fmt.Println("Recording Stopped")
		systray.SetTitle("")
		systray.SetIcon(iconIdle)
	}
	service.OnProcessing = func() {
		systray.SetTitle("Processing...")
	}
	service.OnError = func(err error) {
		log.Printf("Dictation Error: %v", err)
		systray.SetTitle("Dictation: Error")
	}

	// 2. Start Hotkey Listener
	go startHotkeyListener()

	// 3. Handle Quit
	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()
}

func onExit() {
	if service != nil {
		service.Close()
	}
}

func startHotkeyListener() {
	fmt.Println("Listening for hotkeys...")
	// Toggle: Cmd + Shift + Space
	hook.Register(hook.KeyDown, []string{"space", "shift", "command"}, func(e hook.Event) {
		if service != nil {
			service.ToggleRecording()
		}
	})

	// Cancel: Escape
	hook.Register(hook.KeyDown, []string{"esc"}, func(e hook.Event) {
		if service != nil {
			service.StopRecording()
		}
	})

	s := hook.Start()
	<-hook.Process(s)
}
