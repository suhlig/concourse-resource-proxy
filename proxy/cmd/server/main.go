// Copyright 2015 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/gorilla/websocket"
)

var (
	addr      = flag.String("addr", "127.0.0.1:8080", "http service address")
	checkPath string
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 8192

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Time to wait before force close on connection.
	closeGracePeriod = 10 * time.Second
)

func pumpStdin(ws *websocket.Conn, w io.Writer) {
	defer ws.Close()
	ws.SetReadLimit(maxMessageSize)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	for {
		_, message, err := ws.ReadMessage()

		if err != nil {
			break
		}

		log.Printf("I< %s\n", message)

		message = append(message, '\n')

		if _, err := w.Write(message); err != nil {
			break
		}
	}
}

func pumpStdout(r io.Reader, ws *websocket.Conn, done chan struct{}) {
	defer func() {
	}()
	s := bufio.NewScanner(r)
	for s.Scan() {
		ws.SetWriteDeadline(time.Now().Add(writeWait))
		message := s.Bytes()

		log.Printf("O> %s", message)

		if err := ws.WriteMessage(websocket.TextMessage, message); err != nil {
			ws.Close()
			break
		}
	}

	if s.Err() != nil {
		log.Println("scan:", s.Err())
	}

	close(done)

	ws.SetWriteDeadline(time.Now().Add(writeWait))
	ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	time.Sleep(closeGracePeriod)
	ws.Close()
}

func pumpStderr(r io.Reader, done chan struct{}) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		message := s.Bytes()

		log.Printf("E> %s", message)
	}

	if s.Err() != nil {
		log.Println("scan:", s.Err())
	}

	close(done)
}

func ping(ws *websocket.Conn, done chan struct{}) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := ws.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait)); err != nil {
				log.Println("ping:", err)
			}
		case <-done:
			return
		}
	}
}

func internalError(ws *websocket.Conn, msg string, err error) {
	log.Println(msg, err)
	ws.WriteMessage(websocket.TextMessage, []byte("Internal server error."))
}

var upgrader = websocket.Upgrader{}

func serveCheck(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade:", err)
		return
	}

	defer ws.Close()

	stdinReader, stdinWriter, err := os.Pipe()

	if err != nil {
		internalError(ws, "stdin:", err)
		return
	}

	defer stdinReader.Close()
	defer stdinWriter.Close()

	stdoutReader, stdoutWriter, err := os.Pipe()

	if err != nil {
		internalError(ws, "stdout:", err)
		return
	}

	defer stdoutReader.Close()
	defer stdoutWriter.Close()

	stderrReader, stderrWriter, err := os.Pipe()

	if err != nil {
		internalError(ws, "stderr:", err)
		return
	}

	defer stderrReader.Close()
	defer stderrWriter.Close()

	// TODO Pass environment variables to in and out

	proc, err := os.StartProcess(checkPath, flag.Args(), &os.ProcAttr{
		Files: []*os.File{stdinReader, stdoutWriter, stderrWriter},
	})

	if err != nil {
		internalError(ws, "start:", err)
		return
	}

	stdinReader.Close()
	stdoutWriter.Close()
	stderrWriter.Close()

	stdoutDone := make(chan struct{})
	go pumpStdout(stdoutReader, ws, stdoutDone)

	stderrDone := make(chan struct{})
	go pumpStderr(stderrReader, stderrDone)

	go ping(ws, stdoutDone)

	pumpStdin(ws, stdinWriter)

	stdinWriter.Close() // Some commands will exit when stdin is closed.

	// Other commands need a bonk on the head.
	if err := proc.Signal(os.Interrupt); err != nil {
		log.Println("inter:", err)
	}

	select {
	case <-stdoutDone:
	case <-stderrDone:
	case <-time.After(time.Second):
		// A bigger bonk on the head.
		if err := proc.Signal(os.Kill); err != nil {
			log.Println("term:", err)
		}
		<-stdoutDone
	}

	if _, err := proc.Wait(); err != nil {
		log.Println("wait:", err)
	}
}

func main() {
	flag.Parse()
	log.SetFlags(0)

	if len(flag.Args()) < 1 {
		log.Fatal("must specify at least one argument")
	}

	var err error
	checkPath, err = exec.LookPath(flag.Args()[0])

	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/check", serveCheck)

	log.Printf("proxying /check to %s", checkPath)

	log.Fatal(http.ListenAndServe(*addr, nil))
}
