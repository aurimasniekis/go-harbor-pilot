package oauthflow

import "github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"

func credentialSecret(v string) credentials.Secret { return credentials.NewSecret(v) }
