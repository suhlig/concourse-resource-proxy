// Copyright 2015 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"path"
	"time"

	"github.com/gorilla/websocket"
)

var (
	addr         = flag.String("addr", "127.0.0.1:8080", "http service address")
	checkPath    = flag.String("check", "", "path to the check executable under test")
	inPath       = flag.String("in", "", "path to the in executable under test")
	checkProgram string
	inProgram    string
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

func pumpStdin(ws *websocket.Conn, stdin io.Writer) {
	defer ws.Close()
	ws.SetReadLimit(maxMessageSize)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	for {
		_, message, err := ws.ReadMessage()

		if err != nil {
			break
		}

		log.Printf("< %s\n", message)

		message = append(message, '\n')

		if _, err := stdin.Write(message); err != nil {
			break
		}
	}
}

func pumpStdout(stdout io.Reader, ws *websocket.Conn, done chan struct{}, resourceDirectory string) {
	defer func() {
	}()
	s := bufio.NewScanner(stdout)
	for s.Scan() {
		ws.SetWriteDeadline(time.Now().Add(writeWait))
		message := s.Bytes()

		log.Printf("> %s", message)

		if err := ws.WriteMessage(websocket.TextMessage, message); err != nil {
			log.Printf("E: %s", err)
			ws.Close()
			break
		}
	}

	if s.Err() != nil {
		log.Println("scan:", s.Err())
	}

	close(done)

	if resourceDirectory != "" {
		sendFiles(ws, resourceDirectory)
	}

	ws.SetWriteDeadline(time.Now().Add(writeWait))
	ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done reading STDOUT"))
	time.Sleep(closeGracePeriod)
	ws.Close()
}

func pumpStderr(r io.Reader, done chan struct{}) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		message := s.Bytes()

		log.Printf("E %s", message)
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
	ws.WriteMessage(websocket.TextMessage, []byte(msg))
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
	// TODO Pass a temporary directory to in and out as $1

	proc, err := os.StartProcess(checkProgram, []string{checkProgram}, &os.ProcAttr{
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
	go pumpStdout(stdoutReader, ws, stdoutDone, "")
	go ping(ws, stdoutDone)

	stderrDone := make(chan struct{})
	go pumpStderr(stderrReader, stderrDone)

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

func serveIn(w http.ResponseWriter, r *http.Request) {
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

	destination, err := os.MkdirTemp("", "concourse-resource-proxy-server-*")

	if err != nil {
		internalError(ws, "stderr:", err)
		return
	}

	defer os.RemoveAll(destination)

	// TODO Pass environment variables
	proc, err := os.StartProcess(inProgram, []string{inProgram, destination}, &os.ProcAttr{
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
	go pumpStdout(stdoutReader, ws, stdoutDone, destination)
	go ping(ws, stdoutDone)

	stderrDone := make(chan struct{})
	go pumpStderr(stderrReader, stderrDone)

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

	ws.Close()
}

func sendFiles(ws *websocket.Conn, directory string) error {
	files, err := os.ReadDir(directory)

	if err != nil {
		return err
	}

	// this is a bit of a hack - we send STDOUT as websocket.TextMessage
	// and files as websocket.BinaryMessage, so that we can distinguish them.
	// The files are also wrapped in a multipart container so that we can add the
	// meta data (file names etc.).
	w, err := ws.NextWriter(websocket.BinaryMessage)

	if err != nil {
		return err
	}

	writer := multipart.NewWriter(w)

	for _, f := range files {
		content, err := ioutil.ReadFile(path.Join(directory, f.Name()))

		if err != nil {
			log.Printf("Could not read file: %v", err)
		} else {
			log.Printf("Adding part for %v", f.Name())

			part, err := writer.CreatePart(textproto.MIMEHeader{
				"Content-Type":         {"application/octet-stream"},
				"X-Concourse-Filename": {f.Name()},
			})

			if err != nil {
				log.Printf("Could not create part: %v", err)
				continue
			}

			part.Write(content)

			if err != nil {
				log.Printf("Could not write file: %v", err)
				continue
			}
		}
	}

	writer.Close()

	return nil
}

func main() {
	log.SetFlags(0)
	flag.Parse()

	var err error
	checkProgram, err = exec.LookPath(*checkPath)

	if err != nil {
		log.Fatal(err)
	}

	log.Printf("proxying /check to %s", checkProgram)
	http.HandleFunc("/check", serveCheck)

	inProgram, err = exec.LookPath(*inPath)

	if err != nil {
		log.Fatal(err)
	}

	log.Printf("proxying /in to %s", inProgram)
	http.HandleFunc("/in", serveIn)

	log.Fatal(http.ListenAndServe(*addr, nil))
}
