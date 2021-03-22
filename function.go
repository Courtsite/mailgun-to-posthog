package function

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"

	"github.com/kelseyhightower/envconfig"
	"github.com/mailgun/mailgun-go/v4"
	"github.com/mailgun/mailgun-go/v4/events"
	"github.com/posthog/posthog-go"
)

type Config struct {
	PosthogApiKey           string   `split_words:"true" required:"true"`
	PosthogEndpoint         string   `split_words:"true" default:"https://app.posthog.com"`
	MailgunDomain           string   `split_words:"true" required:"true"`
	MailgunPrivateApiKey    string   `split_words:"true" required:"true"`
	UserIdVariableKey       string   `split_words:"true" default:"user_id"`
	AdditionalUserVariables []string `split_words:"true"`
}

func F(w http.ResponseWriter, r *http.Request) {
	var c Config
	err := envconfig.Process("", &c)
	if err != nil {
		log.Fatalf("failed to initialise config: %+v", err)
	}

	ph, err := posthog.NewWithConfig(
		c.PosthogApiKey,
		posthog.Config{
			Endpoint: c.PosthogEndpoint,
			// Verbose: true,
		},
	)
	if err != nil {
		log.Fatalf("failed to initialise posthog: %+v", err)
	}

	mg := mailgun.NewMailgun(c.MailgunDomain, c.MailgunPrivateApiKey)

	var payload mailgun.WebhookPayload
	err = json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		w.WriteHeader(http.StatusNotAcceptable)
		_, _ = w.Write([]byte("invalid request"))
		return
	}

	verified, err := mg.VerifyWebhookSignature(payload.Signature)
	if err != nil {
		log.Printf("failed to verify webhook signature: %+v\n", err)
		w.WriteHeader(http.StatusNotAcceptable)
		_, _ = w.Write([]byte("invalid request"))
		return
	}

	if !verified {
		w.WriteHeader(http.StatusNotAcceptable)
		_, _ = w.Write([]byte("invalid request"))
		return
	}

	e, err := mailgun.ParseEvent(payload.EventData)
	if err != nil {
		log.Printf("failed to parse event data: %+v\n", err)
		w.WriteHeader(http.StatusNotAcceptable)
		_, _ = w.Write([]byte("failed to parse event data"))
		return
	}

	switch event := e.(type) {
	case *events.Delivered:
		distinctId, err := getDistinctID(event.Recipient)
		if err != nil {
			log.Printf("failed to get distinct id: %+v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("failed to get distinct id"))
			return
		}

		if userId := getUserVariable(c.UserIdVariableKey, event.UserVariables); userId != "" {
			distinctId = userId
		}

		properties := posthog.NewProperties().
			Set("mailgun_id", event.ID).
			Set("mailgun_tags", event.Tags).
			Set("mailgun_message_headers", event.Message.Headers).
			Set("mailgun_envelope", event.Envelope)

		for _, key := range c.AdditionalUserVariables {
			v := getUserVariable(key, event.UserVariables)
			if v == "" {
				continue
			}
			properties = properties.Set(key, v)
		}

		err = ph.Enqueue(posthog.Capture{
			DistinctId: distinctId,
			Event:      "mailgun message delivered",
			Timestamp:  event.GetTimestamp(),
			Properties: properties,
		})
		if err != nil {
			log.Printf("failed to enqueue posthog event: %+v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("failed to enqueue posthog event"))
			return
		}

	case *events.Opened:
		distinctId, err := getDistinctID(event.Recipient)
		if err != nil {
			log.Printf("failed to get distinct id: %+v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("failed to get distinct id"))
			return
		}

		if userId := getUserVariable(c.UserIdVariableKey, event.UserVariables); userId != "" {
			distinctId = userId
		}

		properties := posthog.NewProperties().
			Set("mailgun_id", event.ID).
			Set("mailgun_tags", event.Tags).
			Set("mailgun_message_headers", event.Message.Headers).
			Set("mailgun_client_info", event.ClientInfo).
			Set("mailgun_geolocation", event.GeoLocation).
			Set("$ip", event.IP)

		for _, key := range c.AdditionalUserVariables {
			v := getUserVariable(key, event.UserVariables)
			if v == "" {
				continue
			}
			properties = properties.Set(key, v)
		}

		err = ph.Enqueue(posthog.Capture{
			DistinctId: distinctId,
			Event:      "mailgun message opened",
			Timestamp:  event.GetTimestamp(),
			Properties: properties,
		})
		if err != nil {
			log.Printf("failed to enqueue posthog event: %+v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("failed to enqueue posthog event"))
			return
		}

	case *events.Clicked:
		distinctId, err := getDistinctID(event.Recipient)
		if err != nil {
			log.Printf("failed to get distinct id: %+v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("failed to get distinct id"))
			return
		}

		if userId := getUserVariable(c.UserIdVariableKey, event.UserVariables); userId != "" {
			distinctId = userId
		}

		properties := posthog.NewProperties().
			Set("mailgun_id", event.ID).
			Set("mailgun_tags", event.Tags).
			Set("mailgun_message_headers", event.Message.Headers).
			Set("mailgun_url", event.Url).
			Set("mailgun_client_info", event.ClientInfo).
			Set("mailgun_geolocation", event.GeoLocation).
			Set("$ip", event.IP)

		for _, key := range c.AdditionalUserVariables {
			v := getUserVariable(key, event.UserVariables)
			if v == "" {
				continue
			}
			properties = properties.Set(key, v)
		}

		err = ph.Enqueue(posthog.Capture{
			DistinctId: distinctId,
			Event:      "mailgun message link clicked",
			Timestamp:  event.GetTimestamp(),
			Properties: properties,
		})
		if err != nil {
			log.Printf("failed to enqueue posthog event: %+v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("failed to enqueue posthog event"))
			return
		}

	default:
		log.Printf("unsupported event: %T\n", event)
		w.WriteHeader(http.StatusNotAcceptable)
		_, _ = w.Write([]byte("unsupported event"))
		return
	}

	err = ph.Close()
	if err != nil {
		log.Printf("failed to close posthog client: %+v\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("failed to close posthog client"))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func getDistinctID(email string) (string, error) {
	h := sha1.New()
	h.Write([]byte(email))
	return hex.EncodeToString(h.Sum(nil)), nil
}

func getUserVariable(key string, userVariables interface{}) string {
	variables, ok := userVariables.(map[string]interface{})
	if !ok {
		return ""
	}

	v, ok := variables[key].(string)
	if !ok {
		return ""
	}

	return v
}
