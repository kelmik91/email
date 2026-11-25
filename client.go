package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Client отправляет письма через SMTP. Для каждого Send открывается отдельное
// соединение, поэтому Client безопасно использовать одновременно из разных goroutine.
type Client struct {
	config Config
	from   mail.Address
}

// NewClient проверяет и сохраняет SMTP-конфигурацию.
func NewClient(config Config) (*Client, error) {
	cfg, from, err := config.normalized()
	if err != nil {
		return nil, err
	}
	return &Client{config: cfg, from: from}, nil
}

// Send отправляет одно письмо. Bcc используется для SMTP-доставки,
// но адреса Bcc не попадают в заголовки письма.
func (c *Client) Send(ctx context.Context, message Message) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	envelope, err := prepareEnvelope(message)
	if err != nil {
		return err
	}
	if estimateMessageSize(message) > c.config.MaxMessageBytes {
		return fmt.Errorf("email: message exceeds configured size limit of %d bytes", c.config.MaxMessageBytes)
	}
	raw, err := buildMIMEMessage(c.from, message)
	if err != nil {
		return err
	}
	if int64(len(raw)) > c.config.MaxMessageBytes {
		return fmt.Errorf("email: encoded message is too large: %d bytes (limit %d)", len(raw), c.config.MaxMessageBytes)
	}

	conn, err := c.dial(ctx)
	if err != nil {
		return fmt.Errorf("email: connect to SMTP server: %w", err)
	}
	// Закрываем соединение при отмене контекста, в том числе во время SMTP-команд.
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	defer conn.Close()

	deadline := time.Now().Add(c.config.Timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("email: set SMTP deadline: %w", err)
	}

	smtpClient, err := smtp.NewClient(conn, c.config.Host)
	if err != nil {
		return fmt.Errorf("email: initialize SMTP client: %w", err)
	}
	defer smtpClient.Close()

	if c.config.TLSMode == TLSStartTLS {
		ok, _ := smtpClient.Extension("STARTTLS")
		if !ok {
			return fmt.Errorf("email: SMTP server does not support STARTTLS")
		}
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: c.config.Host,
		}
		if err := smtpClient.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("email: enable STARTTLS: %w", err)
		}
	}

	if c.config.Username != "" {
		auth := smtp.PlainAuth("", c.config.Username, c.config.Password, c.config.Host)
		if err := smtpClient.Auth(auth); err != nil {
			return fmt.Errorf("email: SMTP authentication: %w", err)
		}
	}

	if err := smtpClient.Mail(c.from.Address); err != nil {
		return fmt.Errorf("email: set SMTP sender: %w", err)
	}
	for _, recipient := range envelope {
		if err := smtpClient.Rcpt(recipient); err != nil {
			return fmt.Errorf("email: add recipient %q: %w", recipient, err)
		}
	}

	writer, err := smtpClient.Data()
	if err != nil {
		return fmt.Errorf("email: begin SMTP message: %w", err)
	}
	if _, err := writer.Write(raw); err != nil {
		return fmt.Errorf("email: write SMTP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("email: finish SMTP message: %w", err)
	}

	// После успешного DATA письмо уже принято сервером. Ошибка QUIT не должна
	// сообщать вызывающему коду, что отправка не состоялась.
	_ = smtpClient.Quit()
	return nil
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	address := net.JoinHostPort(c.config.Host, strconv.Itoa(c.config.Port))
	dialer := net.Dialer{Timeout: c.config.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}

	if c.config.TLSMode != TLSImplicit {
		return conn, nil
	}

	tlsConn := tls.Client(conn, &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: c.config.Host,
	})
	handshakeCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("TLS handshake: %w", err)
	}
	return tlsConn, nil
}

// prepareEnvelope валидирует адреса и формирует уникальный список SMTP-получателей.
func prepareEnvelope(message Message) ([]string, error) {
	if len(message.To)+len(message.Cc)+len(message.Bcc) == 0 {
		return nil, fmt.Errorf("email: at least one recipient is required")
	}
	if message.Text == "" && message.HTML == "" {
		return nil, fmt.Errorf("email: text or HTML body is required")
	}
	if hasHeaderControl(message.Subject) {
		return nil, fmt.Errorf("email: subject contains unsupported control characters")
	}

	seen := make(map[string]struct{})
	var envelope []string
	for _, group := range [][]string{message.To, message.Cc, message.Bcc} {
		for _, raw := range group {
			address, err := parseAddress(raw)
			if err != nil {
				return nil, fmt.Errorf("email: invalid recipient %q: %w", raw, err)
			}
			key := normalizeAddressKey(address.Address)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			envelope = append(envelope, address.Address)
		}
	}

	if message.ReplyTo != "" {
		if _, err := parseAddress(message.ReplyTo); err != nil {
			return nil, fmt.Errorf("email: invalid Reply-To address: %w", err)
		}
	}
	for i, attachment := range message.Attachments {
		if strings.TrimSpace(attachment.Filename) == "" {
			return nil, fmt.Errorf("email: attachment %d has an empty filename", i)
		}
		if strings.ContainsAny(attachment.Filename, "\r\n\x00") {
			return nil, fmt.Errorf("email: attachment %d has an invalid filename", i)
		}
	}
	return envelope, nil
}

// estimateMessageSize консервативно оценивает MIME-размер до создания копий
// вложений и тела в буфере. Quoted-printable может увеличить текст примерно втрое.
func estimateMessageSize(message Message) int64 {
	size := int64(8 * 1024)
	size += int64(len(message.Text)+len(message.HTML)) * 3
	for _, attachment := range message.Attachments {
		encoded := (int64(len(attachment.Data)) + 2) / 3 * 4
		encoded += (encoded/76 + 1) * 2
		size += encoded + int64(len(attachment.Filename)) + 512
	}
	for _, addresses := range [][]string{message.To, message.Cc, message.Bcc} {
		for _, address := range addresses {
			size += int64(len(address)) + 4
		}
	}
	size += int64(len(message.Subject))*3 + int64(len(message.ReplyTo)) + 1024
	return size
}

func parseAddress(raw string) (*mail.Address, error) {
	if hasHeaderControl(raw) {
		return nil, fmt.Errorf("address contains unsupported control characters")
	}
	address, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if address.Address == "" {
		return nil, fmt.Errorf("empty email address")
	}
	return address, nil
}

func hasHeaderControl(value string) bool {
	for _, r := range value {
		if (r < 0x20 && r != '\t') || r == 0x7f {
			return true
		}
	}
	return false
}

func normalizeAddressKey(address string) string {
	local, domain, ok := strings.Cut(address, "@")
	if !ok {
		return address
	}
	return local + "@" + strings.ToLower(domain)
}
