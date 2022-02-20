package main

import (
	"encoding/json"
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
}

type Output struct {
	Source  json.RawMessage   `json:"source"`
	Version map[string]string `json:"version"`
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

	u, err := url.Parse(input.Source.URL)

	if err != nil {
		log.Fatal("parse:", err)
	}

	if !(u.Scheme == "ws" || u.Scheme == "wss") {
		log.Fatal("Error: uri scheme must be ws or wss")
	}

	if !strings.HasSuffix(u.Path, "/") {
		u.Path = u.Path + "/"
	}

	u.Path = u.Path + "check"

	log.Printf("proxying to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)

	if err != nil {
		log.Fatal("dial:", err)
	}

	defer c.Close()

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			_, message, err := c.ReadMessage()

			if err != nil {
				log.Println(err)
				return
			}

			log.Printf("< %s", message)
		}
	}()

	output, err := json.Marshal(Output{
		Source:  input.Source.Proxied,
		Version: input.Version,
	})

	if err != nil {
		log.Fatal(err)
	}

	log.Printf("> %s\n", output)
	err = c.WriteMessage(websocket.TextMessage, output)

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
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
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
