package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gordonklaus/portaudio"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"
)

type WebSocketClient struct {
	Client *websocket.Conn
}

var (
	audioBuffer []int16
	bufferMutex sync.Mutex
)

func NewWebSocketClient() (*WebSocketClient, error) {
	headers := http.Header{}
	apiKey := os.Getenv("OPENAI_API_KEY")
	headers.Set("Authorization", "Bearer "+apiKey)
	headers.Set("openai-beta", "realtime=v1")

	apiWs := "wss://api.openai.com/v1/realtime?model=gpt-4o-realtime-preview-2024-12-17"

	client, _, err := websocket.DefaultDialer.Dial(apiWs, headers)
	if err != nil {
		return nil, err
	}

	return &WebSocketClient{
		Client: client,
	}, nil
}

func (c *WebSocketClient) Close() {
	if c.Client != nil {
		_ = c.Client.Close()
	}
}

func (c *WebSocketClient) ReceiveMessages(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			fmt.Println("end receiving messages")
			return
		default:
			_, msg, err := c.Client.ReadMessage()
			if err != nil {
				fmt.Println("err while receiving messages:", err)
				return
			}

			var response map[string]interface{}
			if err := json.Unmarshal(msg, &response); err == nil {
				switch response["type"] {
				case "response.audio.delta":
					if base64Chunk, ok := response["delta"].(string); ok {
						//	 fmt.Println("receiving audio in base64:", base64Chunk[:10], "...")
						ProcessAudioChunk(base64Chunk)
					}
				case "response.audio_transcript.delta":
					if textChunk, ok := response["delta"].(string); ok {
						fmt.Print(textChunk)
					}
				case "response.output_item.done":
					fmt.Println("\n\nrealtime finished responsing")
				default:
					// fmt.Println("other message type:", response["type"])
				}
			}
		}
	}
}

func (c *WebSocketClient) SendAudio(base64Audio string) error {
	audioMessage := map[string]interface{}{
		// for sending whole audio file
		"type": "conversation.item.create",
		"item": map[string]interface{}{
			"type": "message",
			"role": "user",
			"content": []map[string]interface{}{
				{"type": "input_audio", "audio": base64Audio},
			},
		},
	}
	if err := c.SendMessage(audioMessage); err != nil {
		return err
	}

	// trigger message for realtime to produce response
	responseMessage := map[string]interface{}{
		"type": "response.create",
	}
	if err := c.SendMessage(responseMessage); err != nil {
		return err
	}

	return nil
}

func ProcessAudioChunk(base64Audio string) {
	audioBytes, err := base64.StdEncoding.DecodeString(base64Audio)
	if err != nil {
		fmt.Println("err while decoding: Base64:", err)
		return
	}

	audioSamples := make([]int16, len(audioBytes)/2)
	for i := 0; i < len(audioSamples); i++ {
		audioSamples[i] = int16(audioBytes[i*2]) | (int16(audioBytes[i*2+1]) << 8)
	}

	bufferMutex.Lock()
	audioBuffer = append(audioBuffer, audioSamples...)
	// fmt.Println("audioBuffer updated len:", len(audioBuffer))
	bufferMutex.Unlock()
}

func PlayAudio() {
	portaudio.Initialize()
	defer portaudio.Terminate()

	inputChannel := 0            // no mic
	outputChannel := 1           // speaker
	sampleRate := float64(24000) // samples per second
	bufferSize := 1024           // samples saved in buffer at the same time

	stream, err := portaudio.OpenDefaultStream(
		inputChannel,
		outputChannel,
		sampleRate,
		bufferSize,
		func(out []int16) {
			bufferMutex.Lock()
			if len(audioBuffer) >= len(out) {
				copy(out, audioBuffer[:len(out)])
				audioBuffer = audioBuffer[len(out):]
			} else {
				for i := range out {
					out[i] = 0
				}
			}
			bufferMutex.Unlock()
		},
	)

	if err != nil {
		log.Println("err while opening stream:", err)
	}
	defer stream.Close()

	err = stream.Start()
	if err != nil {
		log.Println("err while starting stream:", err)
	}
	defer stream.Stop()

	// fmt.Println("play audio started")

	for {
		bufferMutex.Lock()
		if len(audioBuffer) == 0 {
			bufferMutex.Unlock()
			time.Sleep(100 * time.Millisecond)
			continue
		}
		bufferMutex.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *WebSocketClient) SendMessage(message interface{}) error {
	jsonData, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return c.Client.WriteMessage(websocket.TextMessage, jsonData)
}

func main() {
	client, err := NewWebSocketClient()
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go client.ReceiveMessages(ctx)
	go PlayAudio()

	// example input audio file in pcm16, 24000 sample rate & mono
	audioData, err := os.ReadFile("question.wav")
	if err != nil {
		log.Println("err while loading audio file:", err)
		return
	}
	base64Audio := base64.StdEncoding.EncodeToString(audioData)

	err = client.SendAudio(base64Audio)
	if err != nil {
		log.Println("err while sending audio:", err)
		return
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	<-signalChan
	cancel()
	client.Close()
}
