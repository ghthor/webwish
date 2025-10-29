package chat

import (
	"strings"
	"time"

	"github.com/ghthor/webtea/mpty/mptymsg"
)

func init() {
	mptymsg.Register(Msg{})
}

const (
	SysNick  = "system"
	HelpNick = "help"
	InfoNick = "info"
	ErrNick  = "error"
)

type Msg struct {
	At time.Time

	Who  string
	Sess string
	Str  string

	nick string

	recId int64
}

var _ mptymsg.Recordable = Msg{}

func (m Msg) TypeName() string {
	return "chat.Msg"
}

func (m Msg) Ts() time.Time {
	return m.At
}

func (m Msg) SetId(id int64) mptymsg.Recordable {
	m.recId = id
	return m
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
		At:   t,
		nick: HelpNick,
		Who:  HelpNick,
		Str:  msg,
	}
}

func InfoMsg(t time.Time, msg string) Msg {
	return Msg{
		At:   t,
		nick: InfoNick,
		Who:  InfoNick,
		Str:  msg,
	}
}

func ErrMsg(t time.Time, err error) Msg {
	return Msg{
		At:   t,
		nick: ErrNick,
		Who:  ErrNick,
		Str:  err.Error(),
	}
}

func SysMsg(t time.Time, msg string) Msg {
	return Msg{
		At:   t,
		nick: SysNick,
		Who:  SysNick,
		Str:  msg,
	}
}
