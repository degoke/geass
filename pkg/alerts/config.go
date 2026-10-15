package alerts

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Load reads the alerting settings from the platform configuration and the Secrets they name. Nothing configured
// is not an error: it is a Config with no channels.
func Load(ctx context.Context, reader client.Reader, namespace string) (Config, error) {
	var config geassv1alpha1.GeassPlatformConfig
	err := reader.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: namespace}, &config)
	if apierrors.IsNotFound(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	spec := config.Spec.Alerting
	loaded := Config{DashboardURL: platform.DashboardURLFromConfig(config)}
	if spec == nil {
		return loaded, nil
	}

	if spec.SMTP != nil {
		password, err := secretValue(ctx, reader, namespace, spec.SMTP.CredentialRef, SMTPPasswordKey)
		if err != nil {
			return Config{}, err
		}
		loaded.SMTP = &SMTP{
			Host: spec.SMTP.Host, Port: smtpPort(spec.SMTP.Port), Security: smtpSecurity(spec.SMTP.Security),
			Username: spec.SMTP.Username, Password: password, From: spec.SMTP.From,
		}
		if spec.SMTP.CredentialRef != nil {
			loaded.SMTP.PasswordSecret = spec.SMTP.CredentialRef.Name
		}
	}

	secrets := map[string][]byte{}
	if len(spec.Channels) > 0 {
		var secret corev1.Secret
		switch err := reader.Get(ctx, client.ObjectKey{Name: ChannelsSecretName, Namespace: namespace}, &secret); {
		case err == nil:
			secrets = secret.Data
		case !apierrors.IsNotFound(err):
			return Config{}, err
		}
	}
	for _, channel := range spec.Channels {
		loaded.Channels = append(loaded.Channels, Channel{
			ID: channel.ID, Name: channel.Name, Type: channel.Type,
			SlackURL:   strings.TrimSpace(string(secrets[channel.ID])),
			Recipients: channel.Recipients, Events: channel.Events, Projects: channel.Projects, Environments: channel.Environments,
			NotifyResolved: channel.NotifyResolved == nil || *channel.NotifyResolved,
			Disabled:       channel.Disabled,
		})
	}
	return loaded, nil
}

func smtpPort(port int32) int {
	if port <= 0 {
		return DefaultSMTPPort
	}
	return int(port)
}

func smtpSecurity(security string) string {
	switch security {
	case SecurityTLS, SecurityNone:
		return security
	default:
		return SecuritySTARTTLS
	}
}

// secretValue reads one key of a Secret the configuration refers to. No reference, or a Secret that is not there
// yet, is an empty value.
func secretValue(ctx context.Context, reader client.Reader, namespace string, ref *corev1.LocalObjectReference, key string) (string, error) {
	if ref == nil || ref.Name == "" {
		return "", nil
	}
	var secret corev1.Secret
	if err := reader.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: namespace}, &secret); err != nil {
		return "", client.IgnoreNotFound(err)
	}
	return string(secret.Data[key]), nil
}
