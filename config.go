package email

import (
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// TLSMode определяет способ защиты SMTP-соединения.
type TLSMode string

const (
	// TLSStartTLS подключается обычным TCP-соединением, затем обязательно
	// повышает его до TLS. Обычно используется с портом 587.
	TLSStartTLS TLSMode = "starttls"

	// TLSImplicit использует TLS сразу при подключении. Обычно порт 465.
	TLSImplicit TLSMode = "tls"

	// TLSNone отключает TLS. Используйте только для локального тестового SMTP.
	TLSNone TLSMode = "none"
)

// Config — настройки SMTP-сервера и адрес отправителя.
type Config struct {
	Host      string
	Port      int
	Username  string
	Password  string
	FromEmail string
	FromName  string
	TLSMode   TLSMode
	Timeout   time.Duration

	// MaxMessageBytes ограничивает размер сформированного MIME-сообщения.
	// Ноль означает значение по умолчанию — 25 MiB.
	MaxMessageBytes int64
}

const (
	defaultTimeout        = 15 * time.Second
	defaultMaxMessageSize = 25 * 1024 * 1024
)

func (c Config) normalized() (Config, mail.Address, error) {
	if c.Host == "" {
		return c, mail.Address{}, fmt.Errorf("email: SMTP host is required")
	}
	if strings.ContainsAny(c.Host, "\r\n\t /\\") {
		return c, mail.Address{}, fmt.Errorf("email: invalid SMTP host %q", c.Host)
	}
	if c.Port < 1 || c.Port > 65535 {
		return c, mail.Address{}, fmt.Errorf("email: SMTP port must be between 1 and 65535")
	}
	if c.FromEmail == "" {
		return c, mail.Address{}, fmt.Errorf("email: sender address is required")
	}

	if hasHeaderControl(c.FromEmail) || hasHeaderControl(c.FromName) {
		return c, mail.Address{}, fmt.Errorf("email: sender fields contain unsupported control characters")
	}

	from, err := mail.ParseAddress(c.FromEmail)
	if err != nil || from.Address == "" {
		return c, mail.Address{}, fmt.Errorf("email: invalid sender address %q", c.FromEmail)
	}
	if c.FromName != "" {
		from.Name = c.FromName
	}

	if c.TLSMode == "" {
		c.TLSMode = TLSStartTLS
	}
	switch c.TLSMode {
	case TLSStartTLS, TLSImplicit, TLSNone:
	default:
		return c, mail.Address{}, fmt.Errorf("email: unsupported TLS mode %q", c.TLSMode)
	}
	if c.Username == "" && c.Password != "" {
		return c, mail.Address{}, fmt.Errorf("email: SMTP password is set but username is empty")
	}
	if c.Username != "" && c.Password == "" {
		return c, mail.Address{}, fmt.Errorf("email: SMTP username is set but password is empty")
	}
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	if c.MaxMessageBytes <= 0 {
		c.MaxMessageBytes = defaultMaxMessageSize
	}

	return c, *from, nil
}
