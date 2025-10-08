package session

import "github.com/aws/aws-sdk-go/aws"

type Session struct {
	Config *aws.Config
}

func NewSession(cfgs ...*aws.Config) (*Session, error) {
	var cfg *aws.Config
	if len(cfgs) > 0 && cfgs[0] != nil {
		cfg = aws.CopyConfig(cfgs[0])
	} else {
		cfg = aws.NewConfig()
	}
	return &Session{Config: cfg}, nil
}
