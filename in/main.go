// Copyright 2015 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type Input struct {
	Source struct {
		URL     string
		Token   string
		Proxied json.RawMessage `json:"proxied"`
	} `json:"source"`
	Version map[string]string `json:"version"`
	Params  map[string]string `json:"params"`
}

type InMessage struct {
	Source  json.RawMessage   `json:"source"`
	Version map[string]string `json:"version"`
	Params  map[string]string `json:"params"`
}

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	var input Input

	err := json.NewDecoder(os.Stdin).Decode(&input)
	if err != nil {
		log.Fatal(err)
	}

	url, err := url.Parse(input.Source.URL)

	if err != nil {
		log.Fatal("parse:", err)
	}

	if !(url.Scheme == "ws" || url.Scheme == "wss") {
		log.Fatal("Error: uri scheme must be ws or wss")
	}

	if !strings.HasSuffix(url.Path, "/") {
		url.Path = url.Path + "/"
	}

	url.Path = url.Path + "in"

	log.Printf("proxying in to %s: ", url.String())

	ws, _, err := websocket.DefaultDialer.Dial(url.String(), nil)

	if err != nil {
		log.Fatal("dial:", err)
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

			log.Printf("I< %s", message)
			fmt.Println(string(message))

			log.Printf("TODO writing files to %s", os.Args[1])
		}
	}()

	output, err := json.Marshal(InMessage{
		Source:  input.Source.Proxied,
		Version: input.Version,
		Params:  input.Params,
	})

	if err != nil {
		log.Fatal(err)
	}

	// TODO Pass environment variables

	log.Printf("I> %s\n", output)
	err = ws.WriteMessage(websocket.TextMessage, output)

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
