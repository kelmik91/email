package email

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"path"
	"strings"
)

func buildMIMEMessage(from mail.Address, message Message) ([]byte, error) {
	var out bytes.Buffer

	writeHeader(&out, "From", from.String())
	if len(message.To) > 0 {
		values, err := formatAddresses(message.To)
		if err != nil {
			return nil, err
		}
		writeHeader(&out, "To", strings.Join(values, ", "))
	} else if len(message.Cc) == 0 {
		// Скрытая копия — единственная группа получателей.
		writeHeader(&out, "To", "undisclosed-recipients:;")
	}
	if len(message.Cc) > 0 {
		values, err := formatAddresses(message.Cc)
		if err != nil {
			return nil, err
		}
		writeHeader(&out, "Cc", strings.Join(values, ", "))
	}
	if message.ReplyTo != "" {
		replyTo, err := parseAddress(message.ReplyTo)
		if err != nil {
			return nil, fmt.Errorf("email: invalid Reply-To address: %w", err)
		}
		writeHeader(&out, "Reply-To", replyTo.String())
	}
	writeHeader(&out, "Subject", mime.QEncoding.Encode("UTF-8", message.Subject))
	writeHeader(&out, "MIME-Version", "1.0")

	var body bytes.Buffer
	if len(message.Attachments) > 0 {
		mixed := multipart.NewWriter(&body)
		writeHeader(&out, "Content-Type", "multipart/mixed; boundary=\""+mixed.Boundary()+"\"")

		if err := writeBodyPart(mixed, message); err != nil {
			return nil, err
		}
		for _, attachment := range message.Attachments {
			if err := writeAttachment(mixed, attachment); err != nil {
				return nil, err
			}
		}
		if err := mixed.Close(); err != nil {
			return nil, fmt.Errorf("email: close multipart message: %w", err)
		}
	} else if message.Text != "" && message.HTML != "" {
		alternative := multipart.NewWriter(&body)
		writeHeader(&out, "Content-Type", "multipart/alternative; boundary=\""+alternative.Boundary()+"\"")
		if err := writeTextPart(alternative, "text/plain", message.Text); err != nil {
			return nil, err
		}
		if err := writeTextPart(alternative, "text/html", message.HTML); err != nil {
			return nil, err
		}
		if err := alternative.Close(); err != nil {
			return nil, fmt.Errorf("email: close alternative body: %w", err)
		}
	} else {
		contentType := "text/plain"
		content := message.Text
		if message.HTML != "" {
			contentType = "text/html"
			content = message.HTML
		}
		writeHeader(&out, "Content-Type", contentType+"; charset=UTF-8")
		writeHeader(&out, "Content-Transfer-Encoding", "quoted-printable")
		qp := quotedprintable.NewWriter(&body)
		if _, err := qp.Write([]byte(content)); err != nil {
			return nil, fmt.Errorf("email: encode body: %w", err)
		}
		if err := qp.Close(); err != nil {
			return nil, fmt.Errorf("email: finish body encoding: %w", err)
		}
	}

	out.WriteString("\r\n")
	if _, err := out.Write(body.Bytes()); err != nil {
		return nil, fmt.Errorf("email: assemble MIME message: %w", err)
	}
	return out.Bytes(), nil
}

func writeBodyPart(writer *multipart.Writer, message Message) error {
	if message.Text != "" && message.HTML != "" {
		var alternativeBody bytes.Buffer
		alternative := multipart.NewWriter(&alternativeBody)
		partHeader := make(textproto.MIMEHeader)
		partHeader.Set("Content-Type", "multipart/alternative; boundary=\""+alternative.Boundary()+"\"")
		part, err := writer.CreatePart(partHeader)
		if err != nil {
			return fmt.Errorf("email: create alternative MIME part: %w", err)
		}
		if err := writeTextPart(alternative, "text/plain", message.Text); err != nil {
			return err
		}
		if err := writeTextPart(alternative, "text/html", message.HTML); err != nil {
			return err
		}
		if err := alternative.Close(); err != nil {
			return fmt.Errorf("email: close alternative MIME part: %w", err)
		}
		if _, err := part.Write(alternativeBody.Bytes()); err != nil {
			return fmt.Errorf("email: write alternative MIME part: %w", err)
		}
		return nil
	}

	contentType := "text/plain"
	content := message.Text
	if message.HTML != "" {
		contentType = "text/html"
		content = message.HTML
	}
	return writeTextPart(writer, contentType, content)
}

func writeTextPart(writer *multipart.Writer, contentType, content string) error {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Type", contentType+"; charset=UTF-8")
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	part, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("email: create text MIME part: %w", err)
	}
	qp := quotedprintable.NewWriter(part)
	if _, err := qp.Write([]byte(content)); err != nil {
		return fmt.Errorf("email: encode text MIME part: %w", err)
	}
	if err := qp.Close(); err != nil {
		return fmt.Errorf("email: finish text MIME part: %w", err)
	}
	return nil
}

func writeAttachment(writer *multipart.Writer, attachment Attachment) error {
	filename := strings.ReplaceAll(attachment.Filename, "\\", "/")
	filename = path.Base(filename)
	if filename == "." || filename == "/" || filename == "" {
		return fmt.Errorf("email: invalid attachment filename %q", attachment.Filename)
	}

	contentType := attachment.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(path.Ext(filename))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		return fmt.Errorf("email: invalid content type %q for attachment %q", contentType, filename)
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Type", mime.FormatMediaType(mediaType, params))
	header.Set("Content-Transfer-Encoding", "base64")
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	header.Set("Content-Disposition", disposition)
	part, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("email: create attachment part %q: %w", filename, err)
	}

	encoded := base64Lines(attachment.Data)
	if _, err := part.Write(encoded); err != nil {
		return fmt.Errorf("email: write attachment %q: %w", filename, err)
	}
	return nil
}

func formatAddresses(rawAddresses []string) ([]string, error) {
	result := make([]string, 0, len(rawAddresses))
	for _, raw := range rawAddresses {
		address, err := parseAddress(raw)
		if err != nil {
			return nil, fmt.Errorf("email: invalid address %q: %w", raw, err)
		}
		result = append(result, address.String())
	}
	return result, nil
}

func writeHeader(out *bytes.Buffer, name, value string) {
	out.WriteString(name)
	out.WriteString(": ")
	out.WriteString(value)
	out.WriteString("\r\n")
}

func base64Lines(data []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(data)
	if encoded == "" {
		return []byte("\r\n")
	}
	var out strings.Builder
	for len(encoded) > 76 {
		out.WriteString(encoded[:76])
		out.WriteString("\r\n")
		encoded = encoded[76:]
	}
	out.WriteString(encoded)
	out.WriteString("\r\n")
	return []byte(out.String())
}
