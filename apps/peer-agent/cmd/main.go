package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type Frame struct {
	Type    string   `json:"type"`
	ID      string   `json:"id,omitempty"`
	Models  []string `json:"models,omitempty"`
	Model   string   `json:"model,omitempty"`
	Body    string   `json:"body,omitempty"`
	Data    string   `json:"data,omitempty"`
	Message string   `json:"message,omitempty"`
}

func main() {
	token := flag.String("token", "", "Peer registration token (required)")
	relayURL := flag.String("relay", "", "openserve relay URL e.g. https://openserve.example.com (required)")
	ollamaURL := flag.String("ollama", "http://localhost:11434", "Local Ollama URL")
	flag.Parse()

	if *token == "" || *relayURL == "" {
		log.Fatal("--token and --relay are required")
	}

	backoff := time.Second
	for {
		if err := run(*token, *relayURL, *ollamaURL); err != nil {
			log.Printf("disconnected: %v — retrying in %v", err, backoff)
			time.Sleep(backoff)
			if backoff < 60*time.Second {
				backoff *= 2
			}
		} else {
			backoff = time.Second
		}
	}
}

func run(token, relayBase, ollamaBase string) error {
	u, err := url.Parse(relayBase)
	if err != nil {
		return fmt.Errorf("invalid relay URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = "/peer-ws/connect"

	header := http.Header{"Authorization": {"Bearer " + token}}
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	log.Printf("connected to relay at %s", u.String())

	models, err := listOllamaModels(ollamaBase)
	if err != nil {
		log.Printf("warning: could not list ollama models: %v", err)
		models = []string{}
	}
	log.Printf("registered models: %v", models)
	modelSet := make(map[string]bool, len(models))
	for _, m := range models {
		modelSet[m] = true
	}

	if err := conn.WriteJSON(Frame{Type: "hello", Models: models}); err != nil {
		return fmt.Errorf("hello: %w", err)
	}

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var f Frame
		if err := json.Unmarshal(msg, &f); err != nil {
			continue
		}

		switch f.Type {
		case "ping":
			conn.WriteJSON(Frame{Type: "pong"})
		case "request":
			go handleRequest(conn, f, modelSet, ollamaBase)
		}
	}
}

func handleRequest(conn *websocket.Conn, f Frame, modelSet map[string]bool, ollamaBase string) {
	send := func(frame Frame) {
		if err := conn.WriteJSON(frame); err != nil {
			log.Printf("send frame error: %v", err)
		}
	}

	if !modelSet[f.Model] {
		send(Frame{Type: "error", ID: f.ID, Message: fmt.Sprintf("model %q not available locally", f.Model)})
		return
	}

	bodyBytes, err := base64.StdEncoding.DecodeString(f.Body)
	if err != nil {
		send(Frame{Type: "error", ID: f.ID, Message: "invalid body encoding"})
		return
	}

	var reqBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqBody); err != nil {
		send(Frame{Type: "error", ID: f.ID, Message: "invalid JSON body"})
		return
	}
	reqBody["model"] = f.Model
	reqBody["stream"] = true
	bodyBytes, _ = json.Marshal(reqBody)

	resp, err := http.Post(ollamaBase+"/v1/chat/completions", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		send(Frame{Type: "error", ID: f.ID, Message: "ollama error: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		send(Frame{Type: "error", ID: f.ID, Message: fmt.Sprintf("ollama %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))})
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			send(Frame{Type: "chunk", ID: f.ID, Data: "\n"})
		} else {
			send(Frame{Type: "chunk", ID: f.ID, Data: line + "\n"})
		}
	}
	send(Frame{Type: "done", ID: f.ID})
}

func listOllamaModels(ollamaBase string) ([]string, error) {
	resp, err := http.Get(ollamaBase + "/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	names := make([]string, len(result.Models))
	for i, m := range result.Models {
		names[i] = m.Name
	}
	return names, nil
}
