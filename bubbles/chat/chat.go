package chat

import (
	"strings"
	"time"
)

const (
	SysNick  = "system"
	HelpNick = "help"
	InfoNick = "info"
)

type Msg struct {
	LocalAt  time.Time
	ServerAt time.Time

	Who  string
	Sess string
	Str  string

	nick string
}

func (m Msg) Id() string {
	return m.Who + " " + m.Sess
}

func (m Msg) Nick() string {
	if m.nick == "" {
		return NickFromWho(m.Who)

	}
	return m.nick
}

func (m Msg) SetNick(s ...string) Msg {
	if m.nick != "" && len(s) == 0 {
		return m
	}
	if len(s) == 0 {
		m.nick = NickFromWho(m.Who)
	} else {
		m.nick = s[0]
	}
	return m
}

func NickFromWho(who string) string {
	nick, _, match := strings.Cut(who, "@")
	if match {
		return nick
	}
	return who
}

func HelpMsg(t time.Time, msg string) Msg {
	return Msg{
		LocalAt: t,
		nick:    HelpNick,
		Who:     HelpNick,
		Str:     msg,
	}
}

func InfoMsg(t time.Time, msg string) Msg {
	return Msg{
		LocalAt: t,
		nick:    InfoNick,
		Who:     InfoNick,
		Str:     msg,
	}
}

func SysMsg(t time.Time, msg string) Msg {
	return Msg{
		ServerAt: t,
		nick:     SysNick,
		Who:      SysNick,
		Str:      msg,
	}
}
