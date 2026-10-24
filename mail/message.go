package mail

import "github.com/goravel/framework/contracts/mail"

type message struct {
	params Params

	htmlView    string
	textView    string
	subjectView string
	with        map[string]any
}

// The value receiver gives each delivery its own state. Applying a mailable or
// rendering a view must not change the configuration stored in PendingMail.
func (r message) prepare(template mail.Template, mailable ...mail.Mailable) (Params, error) {
	if len(mailable) > 0 {
		r.setUsingMailable(mailable[0])
	}

	if err := r.renderViewTemplate(template); err != nil {
		return Params{}, err
	}

	return r.params, nil
}

func (r *message) setUsingMailable(mailable mail.Mailable) {
	if content := mailable.Content(); content != nil {
		if content.Html != "" {
			r.params.HTML = content.Html
		}
		if content.Text != "" {
			r.params.Text = content.Text
		}
		r.htmlView = content.HtmlView
		r.textView = content.TextView
	}

	// With feeds both the body views and the subject view, even without Content.
	if with := mailable.With(); len(with) > 0 {
		r.with = with
	}

	if attachments := mailable.Attachments(); len(attachments) > 0 {
		r.params.Attachments = attachments
	}

	if headers := mailable.Headers(); len(headers) > 0 {
		r.params.Headers = headers
	}

	if envelope := mailable.Envelope(); envelope != nil {
		if envelope.From.Address != "" {
			r.params.FromAddress = envelope.From.Address
			r.params.FromName = envelope.From.Name
		}
		if len(envelope.To) > 0 {
			r.params.To = envelope.To
		}
		if len(envelope.Cc) > 0 {
			r.params.CC = envelope.Cc
		}
		if len(envelope.Bcc) > 0 {
			r.params.BCC = envelope.Bcc
		}
		if envelope.Subject != "" {
			r.params.Subject = envelope.Subject
		}
		r.subjectView = envelope.SubjectView
	}
}

func (r *message) renderViewTemplate(template mail.Template) error {
	if r.subjectView != "" && template != nil {
		subject, err := template.Render(r.subjectView, r.with)
		if err != nil {
			return err
		}
		// A subject becomes a header, so a CR or LF in the render would start
		// a new header. Keep the first non-empty line. Templates end in a
		// newline, so this is the common case, not just the hostile one.
		if subject = sanitizeSubject(subject); subject != "" {
			r.params.Subject = subject
		}
	}

	if r.htmlView != "" && template != nil {
		html, err := template.Render(r.htmlView, r.with)
		if err != nil {
			return err
		}
		r.params.HTML = html
	}

	if r.textView != "" && template != nil {
		text, err := template.Render(r.textView, r.with)
		if err != nil {
			return err
		}
		r.params.Text = text
	}

	return nil
}
