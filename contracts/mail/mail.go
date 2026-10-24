package mail

// Mail configures and sends messages. Builder methods on the mail facade create
// a pending mail; subsequent builder calls update that pending mail.
type Mail interface {
	// Attach attaches files to the Mail.
	Attach(files []string) Mail
	// Bcc adds a "blind carbon copy" address to the Mail.
	Bcc(addresses []string) Mail
	// Cc adds a "carbon copy" address to the Mail.
	Cc(addresses []string) Mail
	// Content set the content of Mail.
	Content(content Content) Mail
	// From set the sender of Mail.
	From(address Address) Mail
	// Headers adds custom headers to the Mail.
	Headers(headers map[string]string) Mail
	// Queue a message. Mailable overrides and rendered views apply only to this job.
	Queue(mailable ...Mailable) error
	// Send a message. Mailable overrides and rendered views apply only to this send.
	Send(mailable ...Mailable) error
	// Subject set the subject of Mail.
	Subject(subject string) Mail
	// To set the recipients of Mail.
	To(addresses []string) Mail
	// With sets the data passed to every view of the Mail.
	With(data map[string]any) Mail
}

type Mailable interface {
	// Attachments set the attachments of Mailable.
	Attachments() []string
	// Content set the content of Mailable.
	Content() *Content
	// Envelope set the envelope of Mailable.
	Envelope() *Envelope
	// Headers adds custom headers to the Mail.
	Headers() map[string]string
	// Queue set the queue of Mailable.
	Queue() *Queue
	// With returns the data passed to every view of the Mailable: the body
	// views of Content and the SubjectView of Envelope alike.
	With() map[string]any
}

type Content struct {
	Html     string
	Text     string
	HtmlView string
	TextView string
}

type Queue struct {
	Connection string
	Queue      string
}

type Address struct {
	Address string
	Name    string
}

type Envelope struct {
	Bcc  []string
	Cc   []string
	From Address
	// Subject is the literal subject. It is used as-is, and acts as the
	// fallback when SubjectView is empty or fails to render.
	Subject string
	// SubjectView is the path of a template rendered with With() to produce
	// the subject. A successful non-empty render overrides Subject.
	SubjectView string
	To          []string
}
