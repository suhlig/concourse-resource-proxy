package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/suhlig/concourse-resource-proxy/models"
)

type OutRequest struct {
	Source struct {
		URL     string
		Token   string
		Proxied json.RawMessage `json:"proxied"`
	} `json:"source"`
	Params map[string]string `json:"params"`
}

type OutMessage struct {
	Source  json.RawMessage   `json:"source"`
	Version map[string]string `json:"version"`
	Params  map[string]string `json:"params"`
}

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	if len(os.Args) < 2 {
		log.Fatal("Missing parameter for the source directory")
	}

	sourceDirectory := os.Args[1]

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	var request OutRequest

	err := json.NewDecoder(os.Stdin).Decode(&request)
	if err != nil {
		log.Fatal(err)
	}

	url, err := url.Parse(request.Source.URL)

	if err != nil {
		log.Fatal("parse:", err)
	}

	if !(url.Scheme == "ws" || url.Scheme == "wss") {
		log.Fatal("Error: uri scheme must be ws or wss")
	}

	if !strings.HasSuffix(url.Path, "/") {
		url.Path = url.Path + "/"
	}

	url.Path = url.Path + "out"

	log.Printf("proxying out to %s: ", url.String())

	ws, response, err := websocket.DefaultDialer.Dial(url.String(), http.Header{
		"Authorization": []string{request.Source.Token},
	})

	if err != nil {
		log.Fatalf("Could not connect: %s (error %v)", err, response.Status)
	}

	ws.SetCloseHandler(func(code int, text string) error {
		log.Printf("ending with code %d: %s", code, text)
		return nil
	})

	defer ws.Close()

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			messageType, message, err := ws.ReadMessage()

			if err != nil {
				if messageType != -1 { // noFrame
					log.Printf("Error: %s", err)
				}
				return
			}

			log.Printf("O< %s", message)
			fmt.Println(string(message))
		}
	}()

	models.SendFiles(ws, sourceDirectory)

	message, err := json.Marshal(OutMessage{
		Source: request.Source.Proxied,
		Params: request.Params,
	})

	if err != nil {
		log.Fatal(err)
	}

	// TODO Pass environment variables
	log.Printf("O> %s\n", message)
	err = ws.WriteMessage(websocket.TextMessage, message)

	if err != nil {
		log.Fatal(err)
	}

	for {
		select {
		case <-done:
			return
		case <-interrupt:
			log.Println("interrupt")

			// Cleanly close the connection by sending a close message and then
			// waiting (with timeout) for the server to close the connection.
			err := ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))

			if err != nil {
				log.Println("write close:", err)
				return
			}

			select {
			case <-done:
			case <-time.After(time.Second):
				log.Println("timeout")
			}
			return
		}
	}
}
