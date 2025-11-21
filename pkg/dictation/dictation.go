package dictation

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	speech "cloud.google.com/go/speech/apiv2"
	"cloud.google.com/go/speech/apiv2/speechpb"
	"github.com/go-vgo/robotgo"
	"github.com/gordonklaus/portaudio"
	"google.golang.org/api/option"
)

const (
	sampleRate      = 16000
	channelCount    = 1
	audioBufferSize = 1024
	modelName       = "chirp_2"
	languageCode    = "en-GB"
	defaultGain     = 32.0
)

// Service handles the dictation logic.
type Service struct {
	projectID      string
	location       string
	client         *speech.Client
	recognizerName string
	
	isRecording    bool
	mu             sync.Mutex
	cancelRecord   context.CancelFunc // Cancels the entire operation (emergency stop)
	stopAudio      context.CancelFunc // Stops audio sending, triggers graceful shutdown
	
	// Callbacks
	OnStart func()
	OnStop  func()
	OnError func(error)
}

// New creates a new Dictation Service.
func New(projectID, location string) (*Service, error) {
	if location == "" {
		location = "us-central1"
	}
	
	ctx := context.Background()
	client, err := speech.NewClient(ctx, option.WithEndpoint(fmt.Sprintf("%s-speech.googleapis.com:443", location)))
	if err != nil {
		return nil, fmt.Errorf("failed to create speech client: %w", err)
	}

	// Initialize PortAudio globally (safe to call multiple times? No, usually once per process)
	// We assume the caller or this package manages it. 
	// For a package, we might want to Init here and Terminate on Close.
	if err := portaudio.Initialize(); err != nil {
		return nil, fmt.Errorf("portaudio init error: %w", err)
	}

	s := &Service{
		projectID: projectID,
		location:  location,
		client:    client,
	}
	
	if err := s.createRecognizer(ctx); err != nil {
		return nil, err
	}
	
	return s, nil
}

// Close cleans up resources.
func (s *Service) Close() {
	s.StopRecording()
	if s.client != nil {
		// Delete recognizer
		if s.recognizerName != "" {
			delReq := &speechpb.DeleteRecognizerRequest{Name: s.recognizerName}
			s.client.DeleteRecognizer(context.Background(), delReq)
		}
		s.client.Close()
	}
	portaudio.Terminate()
}

func (s *Service) createRecognizer(ctx context.Context) error {
	recognizerID := fmt.Sprintf("dictation-cli-%d", time.Now().Unix())
	parent := fmt.Sprintf("projects/%s/locations/%s", s.projectID, s.location)
	recognizerName := fmt.Sprintf("%s/recognizers/%s", parent, recognizerID)
	s.recognizerName = recognizerName

	fmt.Printf("Creating Recognizer: %s\n", recognizerName)
	req := &speechpb.CreateRecognizerRequest{
		Parent:       parent,
		RecognizerId: recognizerID,
		Recognizer: &speechpb.Recognizer{
			Model: modelName,
			LanguageCodes: []string{languageCode},
			DefaultRecognitionConfig: &speechpb.RecognitionConfig{
				DecodingConfig: &speechpb.RecognitionConfig_ExplicitDecodingConfig{
					ExplicitDecodingConfig: &speechpb.ExplicitDecodingConfig{
						Encoding:          speechpb.ExplicitDecodingConfig_LINEAR16,
						SampleRateHertz:   sampleRate,
						AudioChannelCount: channelCount,
					},
				},
			},
		},
	}

	op, err := s.client.CreateRecognizer(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to create recognizer: %w", err)
	}
	if _, err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed to wait for recognizer creation: %w", err)
	}
	return nil
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
	
	// Audio context to control just the audio sending
	audioCtx, stopAudio := context.WithCancel(ctx)
	s.stopAudio = stopAudio
	
	go s.runLoop(ctx, audioCtx)
}

func (s *Service) stopRecordingLocked() {
	if s.OnStop != nil {
		s.OnStop()
	}
	s.isRecording = false
	
	// Graceful stop: Stop audio first, let stream finish processing
	if s.stopAudio != nil {
		s.stopAudio()
		s.stopAudio = nil
	}
	
	// We do NOT cancel s.cancelRecord here immediately.
	// runLoop will cancel it when it finishes.
}

func (s *Service) runLoop(ctx context.Context, audioCtx context.Context) {
	var transcriptBuffer strings.Builder

	// Ensure we output whatever we have when we exit (stop recording)
	defer func() {
		// Ensure main context is cancelled when we exit
		if s.cancelRecord != nil {
			s.cancelRecord()
		}

		finalText := transcriptBuffer.String()
		log.Printf("[Dictation] Stopping. Buffer length: %d. Content: %q", len(finalText), finalText)
		if finalText != "" {
			// Wait a bit for the user to release the hotkeys (Cmd+Shift+Space)
			// to avoid interference (e.g. Shift+Type = All Caps)
			time.Sleep(500 * time.Millisecond)
			
			log.Println("[Dictation] Typing text...")
			// Revert to TypeStr as PasteStr (Cmd+V) can be flaky or affected by held keys.
			robotgo.TypeStr(finalText)
			log.Println("[Dictation] Typing complete.")
		} else {
			log.Println("[Dictation] Buffer empty, nothing to paste.")
		}
	}()

	// Infinite Streaming Loop
	for {
		// Check if we should still be recording (check main context)
		select {
		case <-ctx.Done():
			return
		default:
		}

		// If audio is stopped (graceful shutdown), we shouldn't restart stream
		if audioCtx.Err() != nil {
			return
		}

		if !s.isRecording {
			return
		}

		s.streamLoop(ctx, audioCtx, func(text string) {
			transcriptBuffer.WriteString(text)
		})
		
		// If we are here, the stream ended. 
		// If audio is still active, we loop back and restart (Infinite Streaming).
		if audioCtx.Err() != nil {
			// Audio stopped, so we are done
			return
		}

		// Add a small backoff to prevent tight loops on persistent errors
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Service) streamLoop(ctx context.Context, audioCtx context.Context, onText func(string)) {
	stream, err := s.client.StreamingRecognize(ctx)
	if err != nil {
		if s.OnError != nil {
			s.OnError(fmt.Errorf("failed to start stream: %w", err))
		}
		return
	}

	// Send Config
	// Note: InterimResults = false for "Final Results Only" mode (Stable)
	if err := stream.Send(&speechpb.StreamingRecognizeRequest{
		Recognizer: s.recognizerName,
		StreamingRequest: &speechpb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: &speechpb.StreamingRecognitionConfig{
				Config: &speechpb.RecognitionConfig{
					Features: &speechpb.RecognitionFeatures{
						EnableAutomaticPunctuation: true,
					},
				},
				StreamingFeatures: &speechpb.StreamingRecognitionFeatures{
					InterimResults: false, // Changed to false for stable transcription
				},
			},
		},
	}); err != nil {
		if s.OnError != nil {
			s.OnError(fmt.Errorf("failed to send config: %w", err))
		}
		return
	}

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
		return
	}
	defer paStream.Close()

	// Audio Sender
	// We need a way to stop the sender when the receiver stops or context is done
	sendErrCh := make(chan error, 1)
	
	go func() {
		defer close(sendErrCh)
		for {
			select {
			case <-ctx.Done():
				return
			case <-audioCtx.Done():
				// Audio stopped, close send to tell Google we are done
				log.Println("[Dictation] Audio stopped, closing send...")
				stream.CloseSend()
				return
			default:
				if err := paStream.Read(); err != nil {
					if err != portaudio.InputOverflowed {
						// log error?
					}
				}
				
				// Gain Boost
				for i, sample := range framesPerBuffer {
					boosted := float64(sample) * defaultGain
					if boosted > 32767 {
						boosted = 32767
					} else if boosted < -32768 {
						boosted = -32768
					}
					framesPerBuffer[i] = int16(boosted)
				}
				
				audioBytes := make([]byte, len(framesPerBuffer)*2)
				for i, sample := range framesPerBuffer {
					audioBytes[i*2] = byte(sample)
					audioBytes[i*2+1] = byte(sample >> 8)
				}
				
				if err := stream.Send(&speechpb.StreamingRecognizeRequest{
					StreamingRequest: &speechpb.StreamingRecognizeRequest_Audio{
						Audio: audioBytes,
					},
				}); err != nil {
					// Stream closed or error
					sendErrCh <- err
					return
				}
			}
		}
	}()

	// Receiver & Typer
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Check if it's a context error (user stopped recording)
			if ctx.Err() != nil {
				return
			}
			log.Printf("Stream receive error: %v", err)
			break
		}

		for _, result := range resp.Results {
			if len(result.Alternatives) == 0 {
				continue
			}
			transcript := result.Alternatives[0].Transcript
			isFinal := result.IsFinal
			
			// Only buffer final results
			if isFinal && transcript != "" {
				log.Printf("[Dictation] Got Final Result: %s", transcript)
				onText(transcript + " ")
			}
		}
	}
}
