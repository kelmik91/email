package email

// Message описывает письмо. В To, Cc и Bcc указывайте по одному адресу
// на элемент, например "Иван Иванов <ivan@example.com>".
type Message struct {
	To      []string
	Cc      []string
	Bcc     []string
	ReplyTo string
	Subject string

	// Можно указать Text, HTML или оба поля. Если заданы оба,
	// письмо будет сформировано как multipart/alternative.
	Text string
	HTML string

	Attachments []Attachment
}

// Attachment — вложение, передаваемое в виде байтов.
type Attachment struct {
	Filename    string
	ContentType string // например, "application/pdf"; пустое значение определится по расширению
	Data        []byte
}
