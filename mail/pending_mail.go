package mail

import "github.com/gradii/framework/contracts/mail"

var _ mail.Mail = (*PendingMail)(nil)

// PendingMail holds fluent mail configuration while sharing its Application.
// Each Send or Queue prepares a separate message from this configuration.
type PendingMail struct {
	mailer  *Application
	message message
}

func newPendingMail(mailer *Application) *PendingMail {
	return &PendingMail{mailer: mailer}
}

func (r *PendingMail) Attach(attachments []string) mail.Mail {
	r.message.params.Attachments = attachments

	return r
}

func (r *PendingMail) Bcc(bcc []string) mail.Mail {
	r.message.params.BCC = bcc

	return r
}

func (r *PendingMail) Cc(cc []string) mail.Mail {
	r.message.params.CC = cc

	return r
}

func (r *PendingMail) Content(content mail.Content) mail.Mail {
	r.message.params.HTML = content.Html
	r.message.params.Text = content.Text
	r.message.htmlView = content.HtmlView
	r.message.textView = content.TextView

	return r
}

func (r *PendingMail) From(address mail.Address) mail.Mail {
	r.message.params.FromAddress = address.Address
	r.message.params.FromName = address.Name

	return r
}

func (r *PendingMail) Headers(headers map[string]string) mail.Mail {
	r.message.params.Headers = headers

	return r
}

func (r *PendingMail) Queue(mailable ...mail.Mailable) error {
	return r.mailer.queueMessage(r.message, mailable...)
}

func (r *PendingMail) Send(mailable ...mail.Mailable) error {
	return r.mailer.sendMessage(r.message, mailable...)
}

func (r *PendingMail) Subject(subject string) mail.Mail {
	r.message.params.Subject = subject

	return r
}

func (r *PendingMail) To(to []string) mail.Mail {
	r.message.params.To = to

	return r
}

func (r *PendingMail) With(data map[string]any) mail.Mail {
	r.message.with = data

	return r
}
