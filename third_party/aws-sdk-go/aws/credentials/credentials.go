package credentials

type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

func NewStaticCredentials(id, secret, token string) *Credentials {
	return &Credentials{AccessKeyID: id, SecretAccessKey: secret, SessionToken: token}
}
