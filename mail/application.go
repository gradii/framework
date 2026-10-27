package mail

import (
	"crypto/tls"
	"fmt"
	"net/smtp"

	"github.com/gradii/framework/contracts/config"
	"github.com/gradii/framework/contracts/mail"
	contractsqueue "github.com/gradii/framework/contracts/queue"
	"github.com/gradii/framework/mail/template"
)

// Params represents all parameters needed for sending mail
type Params struct {
	Subject     string            `json:"subject"`
	HTML        string            `json:"html"`
	Text        string            `json:"text"`
	FromAddress string            `json:"from_address"`
	FromName    string            `json:"from_name"`
	To          []string          `json:"to"`
	CC          []string          `json:"cc"`
	BCC         []string          `json:"bcc"`
	Attachments []string          `json:"attachments"`
	Headers     map[string]string `json:"headers"`
}

var _ mail.Mail = (*Application)(nil)

// Application holds the shared dependencies used to send mail.
type Application struct {
	config   config.Config
	queue    contractsqueue.Queue
	template mail.Template
}

func NewApplication(config config.Config, queue contractsqueue.Queue) (*Application, error) {
	templateEngine, err := template.Get(config)
	if err != nil {
		return nil, err
	}

	return &Application{
		config:   config,
		queue:    queue,
		template: templateEngine,
	}, nil
}

func (r *Application) Attach(attachments []string) mail.Mail {
	return newPendingMail(r).Attach(attachments)
}

func (r *Application) Bcc(bcc []string) mail.Mail {
	return newPendingMail(r).Bcc(bcc)
}

func (r *Application) Cc(cc []string) mail.Mail {
	return newPendingMail(r).Cc(cc)
}

func (r *Application) Content(content mail.Content) mail.Mail {
	return newPendingMail(r).Content(content)
}

func (r *Application) With(data map[string]any) mail.Mail {
	return newPendingMail(r).With(data)
}

func (r *Application) From(address mail.Address) mail.Mail {
	return newPendingMail(r).From(address)
}

func (r *Application) Headers(headers map[string]string) mail.Mail {
	return newPendingMail(r).Headers(headers)
}

func (r *Application) Queue(mailable ...mail.Mailable) error {
	return r.queueMessage(message{}, mailable...)
}

func (r *Application) Send(mailable ...mail.Mailable) error {
	return r.sendMessage(message{}, mailable...)
}

func (r *Application) Subject(subject string) mail.Mail {
	return newPendingMail(r).Subject(subject)
}

func (r *Application) To(to []string) mail.Mail {
	return newPendingMail(r).To(to)
}

func (r *Application) queueMessage(message message, mailable ...mail.Mailable) error {
	params, err := message.prepare(r.template, mailable...)
	if err != nil {
		return err
	}

	job := r.queue.Job(NewSendMailJob(r.config), []contractsqueue.Arg{
		{
			Type:  "string",
			Value: params.Subject,
		},
		{
			Type:  "string",
			Value: params.HTML,
		},
		{
			Type:  "string",
			Value: params.Text,
		},
		{
			Type:  "string",
			Value: params.FromAddress,
		},
		{
			Type:  "string",
			Value: params.FromName,
		},
		{
			Type:  "[]string",
			Value: params.To,
		},
		{
			Type:  "[]string",
			Value: params.CC,
		},
		{
			Type:  "[]string",
			Value: params.BCC,
		},
		{
			Type:  "[]string",
			Value: params.Attachments,
		},
		{
			Type:  "[]string",
			Value: convertMapHeadersToSlice(params.Headers),
		},
	})

	if len(mailable) > 0 {
		if queue := mailable[0].Queue(); queue != nil {
			if queue.Connection != "" {
				job.OnConnection(queue.Connection)
			}
			if queue.Queue != "" {
				job.OnQueue(queue.Queue)
			}
		}
	}

	return job.Dispatch()
}

func (r *Application) sendMessage(message message, mailable ...mail.Mailable) error {
	params, err := message.prepare(r.template, mailable...)
	if err != nil {
		return err
	}

	return SendMail(r.config, params)
}

func SendMail(config config.Config, params Params) error {
	e := NewEmail()
	fromAddress, fromName := params.FromAddress, params.FromName
	if fromAddress == "" {
		fromAddress, fromName = config.GetString("mail.from.address"), config.GetString("mail.from.name")
	}

	e.From = fmt.Sprintf("%s <%s>", fromName, fromAddress)
	e.To = params.To
	if len(params.BCC) > 0 {
		e.Bcc = params.BCC
	}
	if len(params.CC) > 0 {
		e.Cc = params.CC
	}
	e.Subject = params.Subject

	if len(params.HTML) > 0 {
		e.HTML = []byte(params.HTML)
	}

	if len(params.Text) > 0 {
		e.Text = []byte(params.Text)
	}

	for _, attach := range params.Attachments {
		if _, err := e.AttachFile(attach); err != nil {
			return err
		}
	}

	for key, val := range params.Headers {
		e.Headers.Add(key, val)
	}

	port := config.GetInt("mail.port")
	switch port {
	case 465:
		return e.SendWithTLS(fmt.Sprintf("%s:%d", config.GetString("mail.host"), config.GetInt("mail.port")),
			LoginAuth(config.GetString("mail.username"), config.GetString("mail.password")),
			&tls.Config{ServerName: config.GetString("mail.host")})
	case 587:
		return e.SendWithStartTLS(fmt.Sprintf("%s:%d", config.GetString("mail.host"), config.GetInt("mail.port")),
			LoginAuth(config.GetString("mail.username"), config.GetString("mail.password")),
			&tls.Config{ServerName: config.GetString("mail.host")})
	default:
		return e.Send(fmt.Sprintf("%s:%d", config.GetString("mail.host"), port),
			LoginAuth(config.GetString("mail.username"), config.GetString("mail.password")))
	}
}

type loginAuth struct {
	username, password string
}

func LoginAuth(username, password string) smtp.Auth {
	return &loginAuth{username, password}
}

func (a *loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(a.username), nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		switch string(fromServer) {
		case "Username:":
			return []byte(a.username), nil
		case "Password:":
			return []byte(a.password), nil
		}
	}
	return nil, nil
}
