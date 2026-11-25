package email

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestBuildMIMEMessageWithHTMLAndAttachment(t *testing.T) {
	from := mail.Address{Name: "CRM", Address: "crm@example.com"}
	message := Message{
		To:      []string{"Иван Иванов <ivan@example.com>"},
		Bcc:     []string{"hidden@example.com"},
		Subject: "Заявка №123",
		Text:    "Обычный текст",
		HTML:    "<b>HTML</b>",
		Attachments: []Attachment{{
			Filename:    "invoice.pdf",
			ContentType: "application/pdf",
			Data:        []byte("test-pdf-content"),
		}},
	}

	got, err := buildMIMEMessage(from, message)
	if err != nil {
		t.Fatalf("buildMIMEMessage() error = %v", err)
	}
	text := string(got)
	for _, want := range []string{
		`From: "CRM" <crm@example.com>`,
		`To: =?utf-8?q?=D0=98=D0=B2=D0=B0=D0=BD_=D0=98=D0=B2=D0=B0=D0=BD=D0=BE=D0=B2?= <ivan@example.com>`,
		"multipart/mixed",
		"multipart/alternative",
		"filename=invoice.pdf",
		"application/pdf",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message does not contain %q", want)
		}
	}
	if strings.Contains(strings.ToLower(text), "bcc:") || strings.Contains(text, "hidden@example.com") {
		t.Error("Bcc recipient leaked into message headers/body")
	}
}

func TestPrepareEnvelopeDeduplicatesRecipients(t *testing.T) {
	got, err := prepareEnvelope(Message{
		To:   []string{"user@example.com"},
		Cc:   []string{"user@EXAMPLE.com", "copy@example.com"},
		Text: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 unique recipients, got %v", got)
	}
}

func TestPrepareEnvelopeRejectsHeaderInjection(t *testing.T) {
	_, err := prepareEnvelope(Message{
		To:      []string{"user@example.com"},
		Subject: "hello\r\nBcc: victim@example.com",
		Text:    "hello",
	})
	if err == nil {
		t.Fatal("expected CRLF injection to be rejected")
	}
}

func TestBase64Lines(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 200)
	got := string(base64Lines(data))
	for _, line := range strings.Split(strings.TrimSuffix(got, "\r\n"), "\r\n") {
		if len(line) > 76 {
			t.Fatalf("base64 line too long: %d", len(line))
		}
	}
}

func TestSendWithLocalSMTPServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	messageReceived := make(chan string, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		writeLine := func(line string) error {
			if _, err := writer.WriteString(line + "\r\n"); err != nil {
				return err
			}
			return writer.Flush()
		}
		if err := writeLine("220 localhost ESMTP"); err != nil {
			serverErr <- err
			return
		}

		var data strings.Builder
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverErr <- err
				return
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if inData {
				if line == "." {
					inData = false
					messageReceived <- data.String()
					if err := writeLine("250 message accepted"); err != nil {
						serverErr <- err
						return
					}
					continue
				}
				data.WriteString(line + "\n")
				continue
			}

			switch {
			case strings.HasPrefix(line, "EHLO "):
				if err := writeLine("250-localhost"); err != nil {
					serverErr <- err
					return
				}
				if err := writeLine("250 SIZE 26214400"); err != nil {
					serverErr <- err
					return
				}
			case strings.HasPrefix(line, "HELO "):
				if err := writeLine("250 localhost"); err != nil {
					serverErr <- err
					return
				}
			case strings.HasPrefix(line, "MAIL FROM:") || strings.HasPrefix(line, "RCPT TO:"):
				if err := writeLine("250 OK"); err != nil {
					serverErr <- err
					return
				}
			case line == "DATA":
				inData = true
				if err := writeLine("354 send message"); err != nil {
					serverErr <- err
					return
				}
			case line == "QUIT":
				_ = writeLine("221 bye")
				return
			default:
				if err := writeLine("500 unexpected command"); err != nil {
					serverErr <- err
					return
				}
			}
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	mailer, err := NewClient(Config{
		Host:      "127.0.0.1",
		Port:      port,
		FromEmail: "crm@example.com",
		TLSMode:   TLSNone,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = mailer.Send(context.Background(), Message{
		To:      []string{"client@example.com"},
		Bcc:     []string{"hidden@example.com"},
		Subject: "Тест CRM",
		Text:    "Письмо отправлено",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	select {
	case received := <-messageReceived:
		parsed, err := mail.ReadMessage(strings.NewReader(received))
		if err != nil {
			t.Fatalf("parse received message: %v", err)
		}
		if !strings.Contains(parsed.Header.Get("To"), "client@example.com") {
			t.Fatalf("recipient missing from To header: %s", received)
		}
		decodedBody, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
		if err != nil {
			t.Fatalf("decode received body: %v", err)
		}
		if !strings.Contains(string(decodedBody), "Письмо отправлено") {
			t.Fatalf("unexpected message body: %s", decodedBody)
		}
		if parsed.Header.Get("Bcc") != "" || strings.Contains(received, "hidden@example.com") {
			t.Fatalf("Bcc leaked into message: %s", received)
		}
	case err := <-serverErr:
		t.Fatalf("SMTP test server error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP test server did not receive a message")
	}
}
