package mail

import (
	"bytes"
	"errors"
	"net"
	netmail "net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/contracts/mail"
	contractsqueue "github.com/gradii/framework/contracts/queue"
	mailtemplate "github.com/gradii/framework/mail/template"
	mocksconfig "github.com/gradii/framework/mocks/config"
	mocksmail "github.com/gradii/framework/mocks/mail"
	mocksqueue "github.com/gradii/framework/mocks/queue"
)

func TestSendDoesNotReuseMailableData(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "application"
		if pending {
			name = "pending mail"
		}
		t.Run(name, func(t *testing.T) {
			config, deliveries := captureSMTP(t, 2)
			views := t.TempDir()
			for path, source := range map[string]string{
				"subject.txt": "Subject for {{.name}}\n",
				"body.html":   "<p>HTML for {{.name}}</p>",
				"body.txt":    "Text for {{.name}}",
			} {
				require.NoError(t, os.WriteFile(filepath.Join(views, path), []byte(source), 0o600))
			}
			attachment := filepath.Join(t.TempDir(), "first.txt")
			require.NoError(t, os.WriteFile(attachment, []byte("attachment"), 0o600))

			app := &Application{config: config, template: mailtemplate.NewHtml(views)}
			var sender mail.Mail = app
			if pending {
				sender = app.To([]string{"to@example.com"})
			}

			require.NoError(t, sender.Send(&stubMailable{
				content: &mail.Content{HtmlView: "body.html", TextView: "body.txt"},
				envelope: &mail.Envelope{
					To:          []string{"to@example.com"},
					Cc:          []string{"cc@example.com"},
					Bcc:         []string{"bcc@example.com"},
					SubjectView: "subject.txt",
				},
				with:        map[string]any{"name": "Alice"},
				attachments: []string{attachment},
				headers:     map[string]string{"X-Message": "first"},
			}))
			require.NoError(t, sender.Send(&stubMailable{
				content: &mail.Content{HtmlView: "body.html", TextView: "body.txt"},
				envelope: &mail.Envelope{
					To:          []string{"to@example.com"},
					SubjectView: "subject.txt",
				},
			}))

			first, second := <-deliveries, <-deliveries
			firstMail, err := netmail.ReadMessage(bytes.NewReader(first.data))
			require.NoError(t, err)
			secondMail, err := netmail.ReadMessage(bytes.NewReader(second.data))
			require.NoError(t, err)

			assert.Equal(t, "Subject for Alice", firstMail.Header.Get("Subject"))
			assert.Contains(t, string(first.data), "<p>HTML for Alice</p>")
			assert.Contains(t, string(first.data), "Text for Alice")
			assert.Equal(t, "first", firstMail.Header.Get("X-Message"))
			assert.Contains(t, string(first.data), "Content-Disposition: attachment")
			assert.ElementsMatch(t, []string{"to@example.com", "cc@example.com", "bcc@example.com"}, first.recipients)

			assert.Equal(t, "Subject for", secondMail.Header.Get("Subject"))
			assert.Contains(t, string(second.data), "<p>HTML for </p>")
			assert.Contains(t, string(second.data), "Text for")
			assert.NotContains(t, string(second.data), "Alice")
			assert.Empty(t, secondMail.Header.Get("Cc"))
			assert.Empty(t, secondMail.Header.Get("X-Message"))
			assert.NotContains(t, string(second.data), "Content-Disposition: attachment")
			assert.Equal(t, []string{"to@example.com"}, second.recipients)
		})
	}
}

func TestPendingMailQueuePreservesConfiguration(t *testing.T) {
	template := mocksmail.NewTemplate(t)
	with := map[string]any{"name": "Default"}
	withAlice := map[string]any{"name": "Alice"}
	template.EXPECT().Render("alice.subject", withAlice).Return("Alice subject", nil).Once()
	template.EXPECT().Render("alice.html", withAlice).Return("<p>Alice</p>", nil).Once()
	template.EXPECT().Render("alice.txt", withAlice).Return("Alice", nil).Once()
	template.EXPECT().Render("default.html", with).Return("<p>Default</p>", nil).Twice()
	template.EXPECT().Render("default.txt", with).Return("Default", nil).Twice()
	queue, deliveries := captureQueuedMail(t, 3)
	app := &Application{queue: queue, template: template}
	sender := app.To([]string{"default@example.com"}).
		From(Address("default-from@example.com", "Default")).
		Cc([]string{"default-cc@example.com"}).
		Bcc([]string{"default-bcc@example.com"}).
		Attach([]string{"default.txt"}).
		Headers(map[string]string{"X-Message": "default"}).
		Subject("Default subject").
		Content(mail.Content{HtmlView: "default.html", TextView: "default.txt"}).
		With(with)

	require.NoError(t, sender.Queue(&stubMailable{
		content: &mail.Content{HtmlView: "alice.html", TextView: "alice.txt"},
		envelope: &mail.Envelope{
			From:        Address("alice-from@example.com", "Alice"),
			To:          []string{"alice@example.com"},
			Cc:          []string{"alice-cc@example.com"},
			Bcc:         []string{"alice-bcc@example.com"},
			SubjectView: "alice.subject",
		},
		with:        withAlice,
		attachments: []string{"alice.txt"},
		headers:     map[string]string{"X-Message": "alice"},
	}))
	// Both an empty mailable and a send without a mailable use the fluent defaults.
	require.NoError(t, sender.Queue(&stubMailable{}))
	require.NoError(t, sender.Queue())

	assert.Equal(t, []any{
		"Alice subject", "<p>Alice</p>", "Alice", "alice-from@example.com", "Alice",
		[]string{"alice@example.com"}, []string{"alice-cc@example.com"}, []string{"alice-bcc@example.com"},
		[]string{"alice.txt"}, []string{"X-Message: alice"},
	}, <-deliveries)
	for range 2 {
		assert.Equal(t, []any{
			"Default subject", "<p>Default</p>", "Default", "default-from@example.com", "Default",
			[]string{"default@example.com"}, []string{"default-cc@example.com"}, []string{"default-bcc@example.com"},
			[]string{"default.txt"}, []string{"X-Message: default"},
		}, <-deliveries)
	}
}

func TestApplicationQueueConcurrentMessages(t *testing.T) {
	const count = 16
	template := mocksmail.NewTemplate(t)
	template.EXPECT().Render("subject.txt", mock.Anything).
		RunAndReturn(func(_ string, data any) (string, error) {
			return data.(map[string]any)["name"].(string), nil
		}).Times(count)
	queue, deliveries := captureQueuedMail(t, count)
	app := &Application{queue: queue, template: template}
	var group sync.WaitGroup
	errs := make([]error, count)
	want := make([]string, count)
	for i := range count {
		want[i] = strconv.Itoa(i)
		group.Go(func() {
			errs[i] = app.Queue(&stubMailable{
				envelope: &mail.Envelope{
					To:          []string{want[i] + "@example.com"},
					SubjectView: "subject.txt",
				},
				with: map[string]any{"name": want[i]},
			})
		})
	}
	group.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	var subjects []string
	for range count {
		delivery := <-deliveries
		subject := delivery[0].(string)
		subjects = append(subjects, subject)
		assert.Equal(t, []string{subject + "@example.com"}, delivery[5])
	}
	assert.ElementsMatch(t, want, subjects)
}

func TestPendingMailRenderFailureDoesNotChangeNextMessage(t *testing.T) {
	for _, method := range []string{"Send", "Queue"} {
		t.Run(method, func(t *testing.T) {
			template := mocksmail.NewTemplate(t)
			with := map[string]any{"name": "Alice"}
			template.EXPECT().Render("subject.txt", with).Return("Alice subject", nil).Once()
			template.EXPECT().Render("body.html", with).Return("<p>Alice</p>", nil).Once()
			template.EXPECT().Render("body.txt", with).Return("", os.ErrNotExist).Once()
			queue, deliveries := captureQueuedMail(t, 1)
			app := &Application{queue: queue, template: template}
			sender := app.To([]string{"default@example.com"}).
				Subject("Default subject").
				Content(mail.Content{Html: "<p>Default</p>", Text: "Default"})
			mailable := &stubMailable{
				content: &mail.Content{HtmlView: "body.html", TextView: "body.txt"},
				envelope: &mail.Envelope{
					SubjectView: "subject.txt",
					Cc:          []string{"alice@example.com"},
				},
				with: with,
			}

			send := sender.Send
			if method == "Queue" {
				send = sender.Queue
			}
			require.ErrorIs(t, send(mailable), os.ErrNotExist)
			require.NoError(t, sender.Queue())
			assert.Equal(t, []any{
				"Default subject", "<p>Default</p>", "Default", "", "",
				[]string{"default@example.com"}, []string(nil), []string(nil), []string(nil), []string(nil),
			}, <-deliveries)
		})
	}
}

func TestPendingMailSubjectViewWithoutContent(t *testing.T) {
	template := mocksmail.NewTemplate(t)
	with := map[string]any{"name": "Alice"}
	template.EXPECT().Render("subject.txt", with).Return("Alice subject", nil).Once()
	queue, deliveries := captureQueuedMail(t, 1)
	app := &Application{queue: queue, template: template}

	require.NoError(t, app.Subject("Fallback").Content(mail.Content{Text: "Body"}).Queue(&stubMailable{
		envelope: &mail.Envelope{SubjectView: "subject.txt"},
		with:     with,
	}))
	assert.Equal(t, []any{
		"Alice subject", "", "Body", "", "",
		[]string(nil), []string(nil), []string(nil), []string(nil), []string(nil),
	}, <-deliveries)
}

func captureQueuedMail(t *testing.T, count int) (*mocksqueue.Queue, <-chan []any) {
	t.Helper()
	queue := mocksqueue.NewQueue(t)
	job := mocksqueue.NewPendingJob(t)
	deliveries := make(chan []any, count)
	queue.EXPECT().Job(mock.Anything, mock.Anything).
		Run(func(_ contractsqueue.Job, args ...[]contractsqueue.Arg) {
			require.Len(t, args, 1)
			values := make([]any, len(args[0]))
			for i, arg := range args[0] {
				values[i] = arg.Value
			}
			deliveries <- values
		}).Return(job).Times(count)
	job.EXPECT().Dispatch().Return(nil).Times(count)

	return queue, deliveries
}

type smtpDelivery struct {
	data       []byte
	recipients []string
}

func captureSMTP(t *testing.T, count int) (*mocksconfig.Config, <-chan smtpDelivery) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	deliveries := make(chan smtpDelivery, count)
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	go func() {
		defer close(done)
		defer close(deliveries)
		for range count {
			conn, err := listener.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Error(err)
				}
				return
			}
			receiveSMTP(t, conn, deliveries)
		}
	}()

	config := mocksconfig.NewConfig(t)
	config.EXPECT().GetString("mail.host").Return("127.0.0.1")
	config.EXPECT().GetInt("mail.port").Return(listener.Addr().(*net.TCPAddr).Port)
	config.EXPECT().GetString("mail.from.address").Return("from@example.com")
	config.EXPECT().GetString("mail.from.name").Return("Sender")
	config.EXPECT().GetString("mail.username").Return("user")
	config.EXPECT().GetString("mail.password").Return("pass")

	return config, deliveries
}

func receiveSMTP(t *testing.T, conn net.Conn, deliveries chan<- smtpDelivery) {
	t.Helper()
	defer func() { _ = conn.Close() }()
	if !assert.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second))) {
		return
	}
	client := textproto.NewConn(conn)
	if !assert.NoError(t, client.PrintfLine("220 localhost ESMTP")) {
		return
	}
	var recipients []string
	for {
		line, err := client.ReadLine()
		if !assert.NoError(t, err) {
			return
		}
		response := "250 OK"
		switch {
		case strings.HasPrefix(line, "EHLO "):
			if !assert.NoError(t, client.PrintfLine("250-localhost")) {
				return
			}
			response = "250 AUTH LOGIN"
		case strings.HasPrefix(line, "AUTH LOGIN"):
			steps := []struct{ challenge, response string }{
				{"VXNlcm5hbWU6", "dXNlcg=="},
				{"UGFzc3dvcmQ6", "cGFzcw=="},
			}
			// LOGIN may include the username as an initial response.
			if username := strings.TrimPrefix(line, "AUTH LOGIN"); username != "" {
				if !assert.Equal(t, " dXNlcg==", username) {
					return
				}
				steps = steps[1:]
			}
			for _, step := range steps {
				if !assert.NoError(t, client.PrintfLine("334 %s", step.challenge)) {
					return
				}
				credentials, err := client.ReadLine()
				if !assert.NoError(t, err) || !assert.Equal(t, step.response, credentials) {
					return
				}
			}
			response = "235 Authentication successful"
		case strings.HasPrefix(line, "HELO "), strings.HasPrefix(line, "MAIL FROM:"):
		case strings.HasPrefix(line, "RCPT TO:"):
			recipients = append(recipients, strings.Trim(strings.TrimPrefix(line, "RCPT TO:"), "<>"))
		case line == "DATA":
			if !assert.NoError(t, client.PrintfLine("354 Send message")) {
				return
			}
			data, err := client.ReadDotBytes()
			if !assert.NoError(t, err) {
				return
			}
			deliveries <- smtpDelivery{data: data, recipients: recipients}
		case line == "QUIT":
			assert.NoError(t, client.PrintfLine("221 Bye"))
			return
		default:
			t.Errorf("unexpected SMTP command: %s", line)
			return
		}
		if !assert.NoError(t, client.PrintfLine("%s", response)) {
			return
		}
	}
}
