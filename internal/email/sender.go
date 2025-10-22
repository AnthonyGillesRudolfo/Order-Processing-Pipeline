package email

import (
	"bytes"
	"fmt"
	"log"
	"net/smtp"
	"os"
	"text/template"
)

type Sender interface {
	Send(to, subject, htmlBody string) error
}

type SMTPSender struct {
	host string
	port string
	from string
	auth smtp.Auth // nil for local dev (MailHog)
}

func NewSMTPSender() *SMTPSender {
	return &SMTPSender{
		host: getenv("SMTP_HOST", "localhost"),
		port: getenv("SMTP_PORT", "1025"),
		from: getenv("SMTP_FROM", "no-reply@example.local"),
		// auth: add when using a real provider (smtp.PlainAuth("", user, pass, host))
	}
}

func (s *SMTPSender) Send(to, subject, htmlBody string) error {
	addr := fmt.Sprintf("%s:%s", s.host, s.port)
	msg := buildRFC822(s.from, to, subject, htmlBody)
	return smtp.SendMail(addr, s.auth, s.from, []string{to}, msg)
}

func buildRFC822(from, to, subject, html string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: text/html; charset=UTF-8\r\n")
	fmt.Fprintf(&buf, "\r\n%s\r\n", html)
	return buf.Bytes()
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" { return v }
	return d
}

// Simple HTML template for OrderCreated
var orderCreatedTpl = template.Must(template.New("orderCreated").Parse(`
<h2>Thanks for your order!</h2>
<p>Order ID: <b>{{.OrderID}}</b></p>
<p>Total: <b>{{printf "USD %.2f" .Total}}</b></p>
<p>Pay here: <a href="{{.InvoiceURL}}">{{.InvoiceURL}}</a></p>
`))

func RenderOrderCreatedEmail(orderID string, total float64, invoiceURL string) string {
	var buf bytes.Buffer
	_ = orderCreatedTpl.Execute(&buf, map[string]any{
		"OrderID": orderID,
		"Total": total,
		"InvoiceURL": invoiceURL,
	})
	return buf.String()
}

func RenderPaymentCompletedEmail(orderID string, total float64, invoiceURL string) string {
	var buf bytes.Buffer
	_ = orderCreatedTpl.Execute(&buf, map[string]any{
		"OrderID": orderID,
		"Total": total,
		"InvoiceURL": invoiceURL,
	})
	return buf.String()
}

func RenderPaymentExpiredEmail(orderID string, total float64, invoiceURL string) string {
	var buf bytes.Buffer
	_ = orderCreatedTpl.Execute(&buf, map[string]any{
		"OrderID": orderID,
		"Total": total,
		"InvoiceURL": invoiceURL,
	})
	return buf.String()
}

// Fallback logger sender (useful for dev without SMTP)
type LogSender struct{}

func (LogSender) Send(to, subject, htmlBody string) error {
	log.Printf("[Email] to=%s subject=%q body=%q", to, subject, htmlBody)
	return nil
}
