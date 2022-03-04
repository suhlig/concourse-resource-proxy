package models

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/textproto"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gorilla/websocket"
)

const concourseFileNameHeader = "X-Concourse-Filename"

func SendFiles(ws *websocket.Conn, baseDir string) error {
	// this is a bit of a hack - we send STDOUT as websocket.TextMessage
	// and files as websocket.BinaryMessage, so that we can distinguish them.
	// The files are also wrapped in a multipart container so that we can add the
	// meta data (file names etc.).
	w, err := ws.NextWriter(websocket.BinaryMessage)

	if err != nil {
		return err
	}

	writer := multipart.NewWriter(w)

	err = filepath.Walk(baseDir, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}

		if info.Mode().IsRegular() {
			relativePath, err := filepath.Rel(baseDir, path)

			if err != nil {
				return err
			}

			writeFile(writer, baseDir, relativePath)
		}

		return nil
	})

	if err != nil {
		log.Fatalf("Could not walk output tree %s: %v", baseDir, err)
	}

	writer.Close()

	return nil
}

func writeFile(writer *multipart.Writer, baseDir, relativePath string) error {
	content, err := ioutil.ReadFile(path.Join(baseDir, relativePath))

	if err != nil {
		log.Printf("Could not read file: %v", err)
	} else {
		part, err := writer.CreatePart(textproto.MIMEHeader{
			"Content-Type":         {"application/octet-stream"},
			"X-Concourse-Filename": {relativePath},
		})

		if err != nil {
			log.Printf("Could not create part: %v", err)
			return err
		}

		part.Write(content)

		if err != nil {
			log.Printf("Could not write file: %v", err)
			return err
		}
	}

	return nil
}

func ReceiveFiles(ws *websocket.Conn, directory, marker string, done chan struct{}) {
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
			log.Printf("%s< %s", marker, message)
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

				fullPath := path.Join(directory, path.Dir(fileName))
				err = os.MkdirAll(fullPath, os.ModePerm)

				if err != nil {
					log.Println(err)
					continue
				}

				partFile := path.Join(fullPath, path.Base(fileName))
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
}

func getBoundary(message []byte) string {
	line0 := strings.Split(string(message), "\r\n")[0]
	withoutPrefix := strings.TrimPrefix(line0, "--")
	return strings.TrimSuffix(withoutPrefix, "--")
}
