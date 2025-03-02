package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"os"
	"os/signal"
)

type WebSocketClient struct {
	Client *websocket.Conn
}

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
					//if base64Chunk, ok := response["delta"].(string); ok {
					//	 fmt.Println("receiving audio in base64:", base64Chunk[:10], "...")
					//}
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
