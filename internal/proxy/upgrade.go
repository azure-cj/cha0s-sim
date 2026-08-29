package proxy

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
)

func IsWebSocketUpgrade(req *http.Request) bool {
	connection := strings.ToLower(req.Header.Get("Connection"))
	upgrade := req.Header.Get("Upgrade")
	return strings.Contains(connection, "upgrade") && strings.EqualFold(upgrade, "websocket")
}

func HandleUpgrade(w http.ResponseWriter, req *http.Request, targetHost string) error {
	backend, err := net.Dial("tcp", targetHost)
	if err != nil {
		return fmt.Errorf("failed to dial backend: %w", err)
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		backend.Close()
		return fmt.Errorf("webserver doesn't support hijacking")
	}

	client, _, err := hijacker.Hijack()
	if err != nil {
		backend.Close()
		return fmt.Errorf("failed to hijack client connection: %w", err)
	}

	defer backend.Close()
	defer client.Close()

	if err := writeUpgradeRequest(backend, req); err != nil {
		return fmt.Errorf("failed to forward upgrade request: %w", err)
	}

	done := make(chan struct{}, 1)

	go func() {
		if _, err := io.Copy(client, backend); err != nil {
			log.Printf("proxy upgrade copy backend->client error: %v", err)
		}
		done <- struct{}{}
	}()

	go func() {
		if _, err := io.Copy(backend, client); err != nil {
			log.Printf("proxy upgrade copy client->backend error: %v", err)
		}
		done <- struct{}{}
	}()

	<-done
	return nil
}

func writeUpgradeRequest(conn net.Conn, req *http.Request) error {
	if _, err := fmt.Fprintf(conn, "%s %s %s\r\n", req.Method, req.URL.RequestURI(), req.Proto); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(conn, "Host: %s\r\n", req.Host); err != nil {
		return err
	}
	for key, values := range req.Header {
		for _, value := range values {
			if _, err := fmt.Fprintf(conn, "%s: %s\r\n", key, value); err != nil {
				return err
			}
		}
	}
	_, err := io.WriteString(conn, "\r\n")
	return err
}
