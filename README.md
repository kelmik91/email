# email — SMTP-пакет для CRM

Пакет не зависит от сторонних библиотек. Поддерживает SMTP AUTH, STARTTLS, TLS на этапе подключения, текстовые и HTML-письма, To/Cc/Bcc, Reply-To и вложения.

## Подключение

Скопируй каталог `email` в проект, например в `internal/email`.

```go
mailer, err := email.NewClient(email.Config{
    Host:       os.Getenv("SMTP_HOST"),
    Port:       587,
    Username:   os.Getenv("SMTP_USERNAME"),
    Password:   os.Getenv("SMTP_PASSWORD"),
    FromEmail:  os.Getenv("SMTP_FROM"),
    FromName:   "Veranda CRM",
    TLSMode:    email.TLSStartTLS,
    Timeout:    15 * time.Second,
})
if err != nil {
    return err
}

err = mailer.Send(ctx, email.Message{
    To:      []string{"client@example.com"},
    Subject: "Ваша заявка принята",
    Text:    "Спасибо! Мы получили вашу заявку.",
    HTML:    `<h2>Заявка принята</h2><p>Спасибо! Мы скоро свяжемся с вами.</p>`,
})
if err != nil {
    return err
}
```

## Вложения

```go
err := mailer.Send(ctx, email.Message{
    To:      []string{"client@example.com"},
    Subject: "Документы по заявке",
    Text:    "Документы во вложении.",
    Attachments: []email.Attachment{{
        Filename:    "invoice.pdf",
        ContentType: "application/pdf",
        Data:        pdfBytes,
    }},
})
```

## SMTP-настройки

- Порт `587`: `TLSMode: email.TLSStartTLS` (режим по умолчанию).
- Порт `465`: `TLSMode: email.TLSImplicit`.
- `TLSMode: email.TLSNone` — только для локального тестового SMTP, не для production.
- Если SMTP-сервер не требует авторизацию, `Username` и `Password` оставь пустыми.

Письмо отправляется синхронно в рамках вызова `Send`. Для массовых рассылок и отправки из HTTP-обработчиков лучше вызывать пакет через фоновые задачи/очередь, чтобы не держать HTTP-запрос открытым.
