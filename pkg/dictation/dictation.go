package dictation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/go-vgo/robotgo"
	"github.com/gordonklaus/portaudio"
)

const (
	sampleRate      = 16000
	channelCount    = 1
	audioBufferSize = 1024
	modelName       = "models/gemini-2.5-flash-lite-preview-09-2025"
	defaultGain     = 32.0
)

// Service handles the dictation logic.
type Service struct {
	apiKey string

	isRecording  bool
	mu           sync.Mutex
	cancelRecord context.CancelFunc // Cancels the entire operation (emergency stop)
	stopAudio    context.CancelFunc // Stops audio recording, triggers transcription
	httpClient   *http.Client

	// Callbacks
	OnStart      func()
	OnStop       func()
	OnProcessing func()
	OnFinish     func()
	OnError      func(error)
}

// New creates a new Dictation Service.
func New(apiKey string) (*Service, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	// Initialize PortAudio globally
	if err := portaudio.Initialize(); err != nil {
		return nil, fmt.Errorf("portaudio init error: %w", err)
	}

	s := &Service{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}

	return s, nil
}

// Close cleans up resources.
func (s *Service) Close() {
	s.StopRecording()
	portaudio.Terminate()
}

// ToggleRecording starts or stops recording.
func (s *Service) ToggleRecording() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isRecording {
		s.stopRecordingLocked()
	} else {
		s.startRecordingLocked()
	}
}

// StopRecording stops recording if active.
func (s *Service) StopRecording() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isRecording {
		s.stopRecordingLocked()
	}
}

func (s *Service) startRecordingLocked() {
	if s.OnStart != nil {
		s.OnStart()
	}
	s.isRecording = true
	
	// Main context for the whole operation
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelRecord = cancel
	
	// Audio context to control just the audio recording
	audioCtx, stopAudio := context.WithCancel(ctx)
	s.stopAudio = stopAudio
	
	go s.runLoop(ctx, audioCtx, cancel)
}

func (s *Service) stopRecordingLocked() {
	if s.OnStop != nil {
		s.OnStop()
	}
	s.isRecording = false
	
	// Stop audio recording, which will trigger transcription in runLoop
	if s.stopAudio != nil {
		s.stopAudio()
		s.stopAudio = nil
	}
}

func (s *Service) runLoop(ctx context.Context, audioCtx context.Context, cancel context.CancelFunc) {
	var audioData []int16

	// Ensure we clean up
	defer func() {
		if s.OnFinish != nil {
			s.OnFinish()
		}
		cancel()
	}()

	// Audio Setup
	sampleRateFloat := float64(sampleRate)
	framesPerBuffer := make([]int16, audioBufferSize)

	paStream, err := portaudio.OpenDefaultStream(channelCount, 0, sampleRateFloat, len(framesPerBuffer), framesPerBuffer)
	if err != nil {
		if s.OnError != nil {
			s.OnError(fmt.Errorf("failed to open PA stream: %w", err))
		}
		return
	}

	if err := paStream.Start(); err != nil {
		if s.OnError != nil {
			s.OnError(fmt.Errorf("failed to start PA stream: %w", err))
		}
		paStream.Close()
		return
	}
	
	// Recording Loop
	recording := true
	for recording {
		select {
		case <-ctx.Done():
			recording = false
		case <-audioCtx.Done():
			recording = false
		default:
			if err := paStream.Read(); err != nil {
				if err != portaudio.InputOverflowed {
					log.Printf("PortAudio read error: %v", err)
				}
			}

			// Gain Boost and Append
			for _, sample := range framesPerBuffer {
				boosted := float64(sample) * defaultGain
				if boosted > 32767 {
					boosted = 32767
				} else if boosted < -32768 {
					boosted = -32768
				}
				audioData = append(audioData, int16(boosted))
			}
		}
	}

	paStream.Stop()
	paStream.Close()

	// If we were cancelled (emergency stop), don't transcribe
	if ctx.Err() != nil && audioCtx.Err() == nil {
		return
	}

	// Transcribe
	if len(audioData) > 0 {
		if s.OnProcessing != nil {
			s.OnProcessing()
		}
		text, err := s.transcribeAudio(audioData)
		if err != nil {
			if s.OnError != nil {
				s.OnError(fmt.Errorf("transcription failed: %w", err))
			}
			return
		}

		if text != "" {
			// Wait a bit for keys to be released
			time.Sleep(200 * time.Millisecond)
			robotgo.TypeStr(text)
		}
	}
}

func (s *Service) transcribeAudio(samples []int16) (string, error) {
	var audioBytes []byte
	var mimeType string
	var err error

	// Try to compress to MP3 if ffmpeg is available
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		audioBytes, err = compressToMP3(samples, sampleRate)
		if err == nil {
			mimeType = "audio/mp3"
		}
	}

	// Fallback to WAV
	if mimeType == "" {
		audioBytes, err = encodeWAV(samples, sampleRate)
		if err != nil {
			return "", fmt.Errorf("failed to encode WAV: %w", err)
		}
		mimeType = "audio/wav"
	}

	// Prepare JSON payload
	encodedAudio := base64.StdEncoding.EncodeToString(audioBytes)
	
	reqBody := map[string]interface{}{
		"contents": []interface{}{
			map[string]interface{}{
				"parts": []interface{}{
					map[string]interface{}{
						"text": "You are a professional transcriber for a software developer. Strictly transcribe the speech in the audio, expecting technical terminology. Output ONLY the transcription. Do not add any conversational filler. Do not reply to the content. If the audio is unclear, output nothing.",
					},
					map[string]interface{}{
						"inline_data": map[string]interface{}{
							"mime_type": mimeType,
							"data":      encodedAudio,
						},
					},
				},
			},
		},
		"generation_config": map[string]interface{}{
			"response_modalities": []string{"TEXT"},
			"temperature":         0.0,
			"max_output_tokens":   256,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/%s:generateContent?key=%s", modelName, s.apiKey)
	
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract text
	// Response structure: candidates[0].content.parts[0].text
	if candidates, ok := response["candidates"].([]interface{}); ok && len(candidates) > 0 {
		if candidate, ok := candidates[0].(map[string]interface{}); ok {
			if content, ok := candidate["content"].(map[string]interface{}); ok {
				if parts, ok := content["parts"].([]interface{}); ok && len(parts) > 0 {
					if part, ok := parts[0].(map[string]interface{}); ok {
						if text, ok := part["text"].(string); ok {
							return text, nil
						}
					}
				}
			}
		}
	}

	return "", nil
}

func compressToMP3(samples []int16, sampleRate int) ([]byte, error) {
	cmd := exec.Command("ffmpeg", 
		"-f", "s16le", 
		"-ar", strconv.Itoa(sampleRate), 
		"-ac", "1", 
		"-i", "pipe:0", 
		"-ar", "8000", // Downsample to 8kHz
		"-f", "mp3", 
		"-map_metadata", "-1", // Strip metadata
		"-b:a", "8k", // 8kbps for maximum compression
		"pipe:1")
	
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	
	go func() {
		defer stdin.Close()
		// Convert []int16 to []byte (Little Endian)
		buf := make([]byte, len(samples)*2)
		for i, sample := range samples {
			buf[i*2] = byte(sample)
			buf[i*2+1] = byte(sample >> 8)
		}
		stdin.Write(buf)
	}()
	
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg error: %v, stderr: %s", err, stderr.String())
	}
	
	return out.Bytes(), nil
}

func encodeWAV(samples []int16, sampleRate int) ([]byte, error) {
	buf := new(bytes.Buffer)

	// WAV Header
	// RIFF chunk
	buf.WriteString("RIFF")
	totalDataLen := len(samples) * 2
	fileSize := 36 + totalDataLen
	binary.Write(buf, binary.LittleEndian, int32(fileSize))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, int32(16)) // Chunk size
	binary.Write(buf, binary.LittleEndian, int16(1))  // Audio format (1 = PCM)
	binary.Write(buf, binary.LittleEndian, int16(1))  // Num channels
	binary.Write(buf, binary.LittleEndian, int32(sampleRate))
	byteRate := sampleRate * 1 * 16 / 8
	binary.Write(buf, binary.LittleEndian, int32(byteRate))
	blockAlign := 1 * 16 / 8
	binary.Write(buf, binary.LittleEndian, int16(blockAlign))
	binary.Write(buf, binary.LittleEndian, int16(16)) // Bits per sample

	// data chunk
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, int32(totalDataLen))

	// Write samples
	for _, sample := range samples {
		binary.Write(buf, binary.LittleEndian, sample)
	}

	return buf.Bytes(), nil
}