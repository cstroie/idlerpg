// drifter: a minimal idle IRC client for Void Drift.
// Connects to the IRC server, joins the channel, sends !login, and idles.
// Channel messages are printed to stdout with ANSI colours; the log file
// (if any) receives plain stripped text.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	irc "github.com/fluffle/goirc/client"
)

// IRC colour index → ANSI foreground escape (approximate).
var ircToANSI = [16]string{
	"37",  // 0  white
	"30",  // 1  black
	"34",  // 2  dark blue
	"32",  // 3  dark green
	"31",  // 4  red
	"31",  // 5  dark red
	"35",  // 6  dark magenta
	"33",  // 7  orange
	"93",  // 8  yellow
	"92",  // 9  bright green
	"36",  // 10 teal
	"96",  // 11 cyan
	"94",  // 12 bright blue
	"95",  // 13 bright magenta
	"90",  // 14 dark grey
	"37",  // 15 light grey
}

const ansiReset = "\x1b[0m"

// toANSI converts IRC formatting codes to ANSI escape sequences.
func toANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		ch := s[i]
		switch ch {
		case 0x02: // bold
			b.WriteString("\x1b[1m")
			i++
		case 0x1D: // italic
			b.WriteString("\x1b[3m")
			i++
		case 0x1F: // underline
			b.WriteString("\x1b[4m")
			i++
		case 0x1E: // strikethrough
			b.WriteString("\x1b[9m")
			i++
		case 0x16: // reverse
			b.WriteString("\x1b[7m")
			i++
		case 0x0F: // reset
			b.WriteString(ansiReset)
			i++
		case 0x03: // colour: \x03[fg][,bg]
			i++
			fg, bg := -1, -1
			if i < len(s) && s[i] >= '0' && s[i] <= '9' {
				n := 1
				if i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
					n = 2
				}
				fg, _ = strconv.Atoi(s[i : i+n])
				i += n
			}
			if i < len(s) && s[i] == ',' {
				i++
				if i < len(s) && s[i] >= '0' && s[i] <= '9' {
					n := 1
					if i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
						n = 2
					}
					bg, _ = strconv.Atoi(s[i : i+n])
					i += n
				}
			}
			if fg < 0 && bg < 0 {
				b.WriteString(ansiReset)
			} else {
				if fg >= 0 && fg < 16 {
					fmt.Fprintf(&b, "\x1b[%sm", ircToANSI[fg])
				}
				if bg >= 0 && bg < 16 {
					fmt.Fprintf(&b, "\x1b[%sm", "4"+ircToANSI[bg][1:])
				}
			}
		case 0x04, 0x00, '\r', '\n': // skip hex-colour and control junk
			i++
		default:
			b.WriteByte(ch)
			i++
		}
	}
	if strings.Contains(b.String(), "\x1b[") {
		b.WriteString(ansiReset)
	}
	return b.String()
}

var (
	ircColorRe         = regexp.MustCompile(`\x03[0-9]{0,2}(?:,[0-9]{1,2})?`)
	ircControlReplacer = strings.NewReplacer(
		"\x02", "", "\x04", "", "\x0F", "", "\x16", "",
		"\x1D", "", "\x1E", "", "\x1F", "",
		"\r", "", "\n", "", "\x00", "",
	)
)

func stripIRC(s string) string {
	s = ircColorRe.ReplaceAllString(s, "")
	return ircControlReplacer.Replace(s)
}

func main() {
	server       := flag.String("server",        "irc.libera.chat:6667", "IRC server host:port")
	nick         := flag.String("nick",          "",           "IRC nick (required)")
	gamePass     := flag.String("game-pass",     "",           "Game password for !login (required)")
	channel      := flag.String("channel",       "#voidrift",  "Channel to join")
	ssl          := flag.Bool("ssl",             false,        "Use SSL")
	serverPass   := flag.String("server-pass",   "",           "IRC server password")
	nickservPass := flag.String("nickserv-pass",  "",          "NickServ IDENTIFY password")
	botNick      := flag.String("bot",           "VoidKeeper", "Bot nick to send !login to")
	logFile      := flag.String("log",           "",           "Log file path (appended; empty = stdout only)")
	flag.Parse()

	if *nick == "" {
		fmt.Fprintln(os.Stderr, "drifter: -nick is required")
		os.Exit(1)
	}
	if *gamePass == "" {
		fmt.Fprintln(os.Stderr, "drifter: -game-pass is required")
		os.Exit(1)
	}

	// logger goes to stdout (and file if set) for system messages.
	// IRC message text is written separately: ANSI to stdout, plain to file.
	var logFileWriter io.Writer
	if *logFile != "" {
		f, err := os.OpenFile(*logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatalf("drifter: cannot open log file: %v", err)
		}
		defer f.Close()
		logFileWriter = f
	}
	logWriter := io.Writer(os.Stdout)
	if logFileWriter != nil {
		logWriter = io.MultiWriter(os.Stdout, logFileWriter)
	}
	logger := log.New(logWriter, "", log.LstdFlags)

	// logMsg writes an IRC message line: ANSI colour to stdout, plain to file.
	logMsg := func(format, rawText string, args ...any) {
		ts := time.Now().Format("2006/01/02 15:04:05 ")
		prefix := fmt.Sprintf(format, args...)
		fmt.Fprintf(os.Stdout, "%s%s%s\n", ts+prefix, toANSI(rawText), ansiReset)
		if logFileWriter != nil {
			fmt.Fprintf(logFileWriter, "%s%s%s\n", ts+prefix, stripIRC(rawText), "")
		}
	}

	cfg := irc.NewConfig(*nick, "drifter", "Void Drift idle client")
	cfg.SSL = *ssl
	cfg.Server = *server
	cfg.Pass = *serverPass
	cfg.NewNick = func(n string) string { return n + "_" }
	conn := irc.Client(cfg)

	// On SIGINT/SIGTERM, send !logout then exit cleanly (no quit penalty).
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		logger.Println("Shutting down, sending !logout")
		conn.Privmsg(*botNick, "!logout")
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()

	connected := make(chan bool)

	// namesInChannel collects nicks from the NAMES reply for our channel.
	var namesInChannel []string

	// loginSent prevents sending !login more than once per session.
	var loginSent bool

	// loginAck is non-nil while we are waiting for the bot's !login reply.
	var loginAck chan struct{}

	// whoamiPending is true while we are waiting for the !whoami reply.
	var whoamiPending bool

	conn.HandleFunc("connected", func(c *irc.Conn, line *irc.Line) {
		logger.Println("Connected, joining", *channel)
		loginSent = false
		whoamiPending = false
		if *nickservPass != "" {
			c.Privmsg("NickServ", "IDENTIFY "+*nickservPass)
		}
		c.Join(*channel)
	})

	// On our own JOIN: request NAMES to verify the bot is present.
	conn.HandleFunc("JOIN", func(c *irc.Conn, line *irc.Line) {
		if !strings.EqualFold(line.Nick, *nick) {
			return
		}
		target := line.Args[0]
		if !strings.EqualFold(target, *channel) {
			return
		}
		logger.Printf("Joined %s, checking for bot %s", *channel, *botNick)
		namesInChannel = nil
		c.Raw("NAMES " + *channel)
	})

	// 353: NAMREPLY — collect nicks (strip mode prefixes like @, +, ~).
	conn.HandleFunc("353", func(c *irc.Conn, line *irc.Line) {
		if len(line.Args) < 4 {
			return
		}
		if !strings.EqualFold(line.Args[2], *channel) {
			return
		}
		for _, n := range strings.Fields(line.Args[3]) {
			namesInChannel = append(namesInChannel, strings.TrimLeft(n, "@+~&%!"))
		}
	})

	// 366: ENDOFNAMES — bot presence check, then send !login if found.
	conn.HandleFunc("366", func(c *irc.Conn, line *irc.Line) {
		if len(line.Args) < 2 || !strings.EqualFold(line.Args[1], *channel) {
			return
		}
		if loginSent {
			return
		}
		for _, n := range namesInChannel {
			if strings.EqualFold(n, *botNick) {
				loginSent = true
				logger.Printf("Bot %s is in %s, sending !login", *botNick, *channel)
				loginAck = make(chan struct{}, 1)
				ack := loginAck
				c.Privmsg(*botNick, "!login "+*gamePass)
				go func() {
					select {
					case <-ack:
					case <-time.After(10 * time.Second):
						logger.Printf("WARNING: no !login reply from %s after 10s", *botNick)
					}
				}()
				return
			}
		}
		logger.Printf("WARNING: bot %s not found in %s — !login not sent; will retry on next JOIN", *botNick, *channel)
	})

	// 403: No such channel.
	conn.HandleFunc("403", func(c *irc.Conn, line *irc.Line) {
		ch := ""
		if len(line.Args) > 1 {
			ch = line.Args[1]
		}
		logger.Printf("ERROR: channel %s does not exist", ch)
	})

	// 473/474/475: Cannot join (invite-only, banned, wrong key).
	for _, num := range []string{"473", "474", "475"} {
		num := num
		conn.HandleFunc(num, func(c *irc.Conn, line *irc.Line) {
			reason := map[string]string{
				"473": "invite-only",
				"474": "banned",
				"475": "wrong channel key",
			}[num]
			logger.Printf("ERROR: cannot join %s: %s", *channel, reason)
		})
	}

	// Bot joins or parts — log for visibility.
	conn.HandleFunc("JOIN", func(c *irc.Conn, line *irc.Line) {
		if strings.EqualFold(line.Nick, *botNick) && strings.EqualFold(line.Args[0], *channel) {
			logger.Printf("Bot %s joined %s", *botNick, *channel)
		}
	})
	conn.HandleFunc("PART", func(c *irc.Conn, line *irc.Line) {
		if strings.EqualFold(line.Nick, *botNick) && strings.EqualFold(line.Args[0], *channel) {
			logger.Printf("WARNING: bot %s left %s", *botNick, *channel)
		}
	})
	conn.HandleFunc("QUIT", func(c *irc.Conn, line *irc.Line) {
		if strings.EqualFold(line.Nick, *botNick) {
			logger.Printf("WARNING: bot %s quit", *botNick)
		}
	})
	conn.HandleFunc("KICK", func(c *irc.Conn, line *irc.Line) {
		if len(line.Args) >= 2 && strings.EqualFold(line.Args[1], *botNick) {
			logger.Printf("WARNING: bot %s was kicked from %s", *botNick, *channel)
		}
	})

	conn.HandleFunc("PRIVMSG", func(c *irc.Conn, line *irc.Line) {
		target := line.Args[0]
		raw := line.Args[1]
		logMsg("[%s] <%s> ", raw, target, line.Nick)

		text := stripIRC(raw)

		// Watch for !whoami reply to verify we are online.
		if whoamiPending && strings.EqualFold(line.Nick, *botNick) && !strings.HasPrefix(target, "#") &&
			strings.Contains(text, "phase:") {
			whoamiPending = false
			if strings.Contains(text, "[online]") {
				logger.Printf("Online status confirmed: %s", text)
			} else {
				logger.Printf("WARNING: not online after login: %s", text)
			}
		}

		// Watch for the bot's login acknowledgement.
		if loginAck != nil && strings.EqualFold(line.Nick, *botNick) {
			isDM      := !strings.HasPrefix(target, "#")
			isPrivAck := isDM && strings.Contains(text, "logged in.")
			isPrivErr := isDM && !strings.Contains(text, "logged in.")
			isChanAck := !isDM && strings.Contains(text, "enters the void")

			if isPrivAck || isChanAck {
				logger.Printf("Login confirmed: %s", text)
				go func() {
					time.Sleep(5 * time.Second)
					whoamiPending = true
					logger.Println("Sending !whoami to verify online status")
					c.Privmsg(*botNick, "!whoami")
				}()
				select {
				case loginAck <- struct{}{}:
				default:
				}
				loginAck = nil
			} else if isPrivErr {
				logger.Printf("WARNING: login failed: %s", text)
				select {
				case loginAck <- struct{}{}:
				default:
				}
				loginAck = nil
			}
		}
	})

	conn.HandleFunc("NOTICE", func(c *irc.Conn, line *irc.Line) {
		target := line.Args[0]
		logMsg("[%s] -%s- ", line.Args[1], target, line.Nick)
	})

	conn.HandleFunc("disconnected", func(c *irc.Conn, line *irc.Line) {
		logger.Println("Disconnected")
		connected <- false
	})

	for {
		logger.Println("Connecting to", *server)
		if err := conn.Connect(); err != nil {
			logger.Println("Connect error:", err)
			time.Sleep(10 * time.Second)
			continue
		}
		for {
			if ok := <-connected; !ok {
				logger.Println("Reconnecting in 10s...")
				time.Sleep(10 * time.Second)
				break
			}
		}
	}
}
