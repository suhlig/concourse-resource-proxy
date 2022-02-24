// Copyright 2015 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
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

const concourseFileNameHeader = "X-Concourse-Filename"

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	if len(os.Args) < 2 {
		log.Fatal("Missing parameter for the destination directory")
	}

	destination := os.Args[1]

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

	ws, response, err := websocket.DefaultDialer.Dial(url.String(), http.Header{
		"Authorization": []string{input.Source.Token},
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

			switch messageType {
			case websocket.TextMessage:
				log.Printf("I< %s", message)
				fmt.Println(string(message))
			case websocket.BinaryMessage:
				boundary := getBoundary(message) // hack; perhaps create proper Content-Disposition header?
				mr := multipart.NewReader(bytes.NewReader(message), boundary)

				for {
					part, err := mr.NextPart()

					if err == io.EOF {
						return
					}

					if err != nil {
						log.Fatal(err)
					}

					fileName := part.Header.Get(concourseFileNameHeader)

					if fileName == "" {
						log.Printf("Warning: skipping part because it has no %s set", concourseFileNameHeader)
						continue
					}

					partFile := path.Join(destination, fileName)
					f, err := os.Create(partFile)

					if err != nil {
						log.Println(err)
						continue
					}

					defer f.Close()

					bytes, err := io.Copy(f, part)

					if err != nil {
						log.Println(err)
						continue
					}

					log.Printf("Part %q: %d bytes written to %v\n", fileName, bytes, partFile)
				}
			default:
				log.Printf("Unable to handle message type %d", messageType)
			}
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

func getBoundary(message []byte) string {
	line0 := strings.Split(string(message), "\r\n")[0]
	withoutPrefix := strings.TrimPrefix(line0, "--")
	return strings.TrimSuffix(withoutPrefix, "--")
}
