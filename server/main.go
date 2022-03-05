// Copyright 2015 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"flag"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/gorilla/websocket"
	"github.com/suhlig/concourse-resource-proxy/models"
)

var (
	addr          = flag.String("addr", "127.0.0.1:8080", "http service address")
	checkPath     = flag.String("check", "", "path to the `check` executable under test")
	inPath        = flag.String("in", "", "path to the `in` executable under test")
	outPath       = flag.String("out", "", "path to the `out` executable under test")
	requiredToken = flag.String("token", randomToken(), "authentication token")
	checkProgram  string
	inProgram     string
	outProgram    string
	upgrader      = websocket.Upgrader{}
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

func main() {
	log.SetFlags(0)
	flag.Parse()

	var err error
	checkProgram, err = exec.LookPath(*checkPath)

	if err != nil {
		log.Fatal(err)
	}

	log.Printf("requiring token %s", *requiredToken)

	log.Printf("proxying /check to %s", checkProgram)
	http.HandleFunc("/check", serveCheck)

	inProgram, err = exec.LookPath(*inPath)

	if err != nil {
		log.Fatal(err)
	}

	log.Printf("proxying /in to %s", inProgram)
	http.HandleFunc("/in", serveIn)

	outProgram, err = exec.LookPath(*outPath)

	if err != nil {
		log.Fatal(err)
	}

	log.Printf("proxying /out to %s", outProgram)
	http.HandleFunc("/out", serveOut)

	log.Fatal(http.ListenAndServe(*addr, nil))
}

func pumpStdin(ws *websocket.Conn, stdin io.Writer, marker string) {
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

func pumpStdout(stdout io.Reader, ws *websocket.Conn, done chan struct{}, resourceDirectory, marker string) {
	defer func() {
	}()

	s := bufio.NewScanner(stdout)

	// forward lines on STDIN as websocket text message
	for s.Scan() {
		ws.SetWriteDeadline(time.Now().Add(writeWait))
		message := s.Bytes()

		log.Printf("%s> %s", marker, message)

		if err := ws.WriteMessage(websocket.TextMessage, message); err != nil {
			log.Printf("E: %s", err)
			ws.Close()
			break
		}
	}

	if s.Err() != nil {
		log.Println("scan:", s.Err())
	}

	if !isClosed(done) {
		close(done)
	}

	if resourceDirectory != "" {
		models.SendFiles(ws, resourceDirectory)
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

func serveCheck(w http.ResponseWriter, r *http.Request) {
	suppliedToken := r.Header.Get("Authorization")

	if suppliedToken != *requiredToken {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("No or wrong auth token"))
		return
	}

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

	// No environment variables to be passed
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
	go pumpStdout(stdoutReader, ws, stdoutDone, "", "C")
	go ping(ws, stdoutDone)

	stderrDone := make(chan struct{})
	go pumpStderr(stderrReader, stderrDone)

	pumpStdin(ws, stdinWriter, "C")

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
	suppliedToken := r.Header.Get("Authorization")

	if suppliedToken != *requiredToken {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("No or wrong auth token"))
		return
	}

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

	destination, err := os.MkdirTemp("", "concourse-resource-proxy-server-in-*")

	if err != nil {
		internalError(ws, "stderr:", err)
		return
	}

	defer os.RemoveAll(destination)

	// TODO Set received environment variables for inProgram
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
	go pumpStdout(stdoutReader, ws, stdoutDone, destination, "I")
	go ping(ws, stdoutDone)

	stderrDone := make(chan struct{})
	go pumpStderr(stderrReader, stderrDone)

	pumpStdin(ws, stdinWriter, "I")

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

func serveOut(w http.ResponseWriter, r *http.Request) {
	suppliedToken := r.Header.Get("Authorization")

	if suppliedToken != *requiredToken {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("No or wrong auth token"))
		return
	}

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
	stdoutDone := make(chan struct{})

	sourceDirectory, err := os.MkdirTemp("", "concourse-resource-proxy-server-out-*")

	if err != nil {
		internalError(ws, "stderr:", err)
		return
	}

	defer os.RemoveAll(sourceDirectory)

	// receive files and put them into sourceDirectory so that outProgram can do it's thing
	models.ReceiveFiles(ws, sourceDirectory, "O", stdoutDone)

	// TODO Set received environment variables for inProgram
	proc, err := os.StartProcess(outProgram, []string{outProgram, sourceDirectory}, &os.ProcAttr{
		Files: []*os.File{stdinReader, stdoutWriter, stderrWriter},
	})

	if err != nil {
		internalError(ws, "start:", err)
		return
	}

	stdinReader.Close()
	stdoutWriter.Close()
	stderrWriter.Close()

	go pumpStdout(stdoutReader, ws, stdoutDone, "", "O")
	go ping(ws, stdoutDone)

	stderrDone := make(chan struct{})
	go pumpStderr(stderrReader, stderrDone)

	pumpStdin(ws, stdinWriter, "O")

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

// https://stackoverflow.com/a/22892986/3212907
func randomToken() string {
	rand.Seed(time.Now().UnixNano())

	var stock = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")

	b := make([]rune, 32)

	for i := range b {
		b[i] = stock[rand.Intn(len(stock))]
	}

	return string(b)
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
	}

	return false
}
