# Chrisper

A real-time global dictation utility for macOS (Apple Silicon) that streams audio to Google Cloud Speech-to-Text V2 and types the text into your active window.

## Features
*   **Real-time Dictation**: Streams audio to Google Cloud STT V2 (Chirp 2 model).
*   **Live Typing**: Simulates typing with interim results (backspaces and retypes).
*   **System Tray**: Runs in the menu bar with a status indicator.
*   **Global Hotkeys**:
    *   **Toggle Recording**: `Cmd + Shift + Space`
    *   **Cancel Recording**: `Escape`

## Prerequisites

### System Dependencies
This tool requires `portaudio` for audio capture.

```bash
brew install portaudio
```

### Google Cloud Setup
1.  Create a Google Cloud Project.
2.  Enable the **Cloud Speech-to-Text API** (specifically V2).
3.  Set up authentication:
    *   Create a Service Account.
    *   Download the JSON key file.
    *   Set the environment variable:
        ```bash
        export GOOGLE_APPLICATION_CREDENTIALS="/path/to/your/service-account-key.json"
        ```
        *Note: For the .app bundle, you might need to launch it from a shell that has this variable set, or hardcode/configure it within the app.*

## Installation

### Build from Source
1.  Clone the repository.
2.  Run the build script:
    ```bash
    ./build_app.sh
    ```
3.  Move `Chrisper.app` to your Applications folder:
    ```bash
    mv Chrisper.app /Applications/
    ```

## Usage

1.  **Launch**: Open `Chrisper.app` from your Applications folder.
2.  **Permissions**:
    *   The first time you run it, macOS will prompt for **Microphone** access.
    *   It will also likely request **Accessibility** and **Input Monitoring** permissions. You must grant these in **System Settings > Privacy & Security**.
    *   *Tip*: If it doesn't type, remove "Chrisper" from Accessibility/Input Monitoring and add it back.
3.  **Dictate**:
    *   Look for the icon in the menu bar.
    *   Press **Cmd + Shift + Space** to start.
    *   Speak and watch it type!
