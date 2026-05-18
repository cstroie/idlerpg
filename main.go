package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	irc "github.com/fluffle/goirc/client"
)

func main() {
	server := flag.String("server", "irc.libera.chat:6667", "IRC server host:port")
	nick := flag.String("nick", "GoIdle", "Bot nick")
	password := flag.String("password", "", "Server password")
	ssl := flag.Bool("ssl", false, "Use SSL")
	channel := flag.String("channel", "#idlerpg", "Game channel")
	dataFile := flag.String("data", "idlerpg.json", "Player data file")
	guildsFile := flag.String("guilds", "guilds.json", "Guild data file")
	dev := flag.Bool("dev", false, "Dev mode: auto-login channel members on startup and speed up TTL by 5×")
	nickservPass := flag.String("nickserv", "", "NickServ password (sends IDENTIFY on connect)")
	flag.Parse()

	cfg := irc.NewConfig(*nick, "idlerpg", "IdleRPG bot")
	cfg.SSL = *ssl
	cfg.Server = *server
	cfg.Pass = *password
	cfg.NewNick = func(n string) string { return n + "_" }
	conn := irc.Client(cfg)

	say := func(msg string) {
		log.Printf(">> %s", msg)
		conn.Privmsg(*channel, msg)
	}

	game := newGame(*dataFile, *guildsFile, say)
	game.DevMode = *dev
	game.setTopic = func(topic string) {
		log.Printf("TOPIC: %s", topic)
		conn.Topic(*channel, topic)
	}

	connected := make(chan bool)
	registerHandlers(conn, game, say, connected, *channel, *nick, *nickservPass, *dev)

	for {
		log.Println("Connecting to", *server)
		if err := conn.Connect(); err != nil {
			log.Println("Connect error:", err)
			time.Sleep(10 * time.Second)
			continue
		}
		for {
			if ok := <-connected; !ok {
				log.Println("Disconnected, reconnecting in 10s...")
				time.Sleep(10 * time.Second)
				break
			}
		}
	}
}

func registerHandlers(conn *irc.Conn, game *Game, say func(string), connected chan bool,
	channel, botNick, nickservPass string, dev bool) {

	conn.HandleFunc("connected", func(c *irc.Conn, line *irc.Line) {
		log.Println("Connected, joining", channel)
		if nickservPass != "" {
			c.Privmsg("NickServ", "IDENTIFY "+nickservPass)
		}
		c.Join(channel)
		game.start()
		if dev {
			c.Who(channel)
		}
	})

	registerWHOHandlers(conn, game, botNick, dev)

	conn.HandleFunc("JOIN", func(c *irc.Conn, line *irc.Line) {
		if extractNick(line.Src) == botNick {
			return
		}
		game.OnJoin(line.Src)
	})
	conn.HandleFunc("PART", func(c *irc.Conn, line *irc.Line) { game.OnPart(line.Src) })
	conn.HandleFunc("QUIT", func(c *irc.Conn, line *irc.Line) { game.OnQuit(line.Src) })
	conn.HandleFunc("NICK", func(c *irc.Conn, line *irc.Line) { game.OnNick(line.Src, line.Args[0]) })

	conn.HandleFunc("KICK", func(c *irc.Conn, line *irc.Line) {
		if len(line.Args) < 2 {
			return
		}
		if line.Args[1] == botNick {
			c.Join(channel)
			return
		}
		game.OnKick(line.Args[1])
	})

	conn.HandleFunc("PRIVMSG", func(c *irc.Conn, line *irc.Line) {
		if len(line.Args) < 2 {
			return
		}
		src, ch, text := line.Src, line.Args[0], strings.TrimSpace(line.Args[1])
		fields := strings.Fields(text)
		if len(fields) == 0 {
			return
		}
		replyTo := ch
		if ch == botNick {
			replyTo = extractNick(src)
		}
		if ch == channel {
			game.OnPrivmsg(src, text)
			if strings.HasPrefix(text, "!") {
				replyTo = extractNick(src)
				conn.Privmsg(replyTo, "Tip: use PM for bot commands to avoid talk penalties.")
			}
		}
		reply := func(msg string) { conn.Privmsg(replyTo, msg) }
		dispatchCommand(src, fields, game, say, reply)
	})

	conn.HandleFunc("disconnected", func(c *irc.Conn, line *irc.Line) { connected <- false })
}

// registerWHOHandlers wires up the WHO reply (352) and end-of-WHO (315) handlers
// used in dev mode to auto-login existing channel members.
func registerWHOHandlers(conn *irc.Conn, game *Game, botNick string, dev bool) {
	var whoQueue []string
	conn.HandleFunc("352", func(c *irc.Conn, line *irc.Line) {
		if !dev || len(line.Args) < 6 {
			return
		}
		memberNick := line.Args[5]
		if memberNick == botNick {
			return
		}
		whoQueue = append(whoQueue, fmt.Sprintf("%s!%s@%s", memberNick, line.Args[2], line.Args[3]))
	})
	conn.HandleFunc("315", func(c *irc.Conn, line *irc.Line) {
		if !dev {
			return
		}
		queue := whoQueue
		whoQueue = nil
		log.Printf("Auto-login: %d channel member(s) found", len(queue))
		for _, src := range queue {
			game.OnJoin(src)
		}
	})
}

func init() {
	log.Println("IdleRPG bot starting")
}

// dispatchCommand routes a parsed IRC command to the appropriate Game method.
func dispatchCommand(src string, fields []string, g *Game, say, reply func(string)) {
	switch fields[0] {
	case "!register":
		dispatchRegister(src, fields, g, say, reply)
	case "!login":
		dispatchLogin(src, fields, g, reply)
	case "!logout":
		reply(g.CmdLogout(src))
	case "!dualclass":
		dispatchDualClass(src, fields, g, reply)
	case "!align":
		dispatchAlign(src, fields, g, reply)
	case "!status":
		reply(g.CmdStatus(src, optArg(fields, 1)))
	case "!whoami":
		reply(g.CmdStatus(src, ""))
	case "!top":
		reply(g.CmdTop())
	case "!online":
		reply(g.CmdOnline())
	case "!quest":
		reply(g.CmdQuest())
	case "!items":
		reply(g.CmdItems(src, optArg(fields, 1)))
	case "!pos":
		reply(g.CmdPos(src, optArg(fields, 1)))
	case "!help":
		reply(helpText)
	default:
		dispatchGuildCommand(src, fields, g, say, reply)
	}
}

const helpText = "IdleRPG commands: " +
	"!register <nick> <class> <pass> | " +
	"!login <pass> | !logout | " +
	"!dualclass <class> (level 12+, permanent) | " +
	"!align <good|neutral|evil> | " +
	"!status [nick] | !whoami | !top | !online | !quest | !items [nick] | !pos [nick] | " +
	"!gcreate <name> | !ginvite <nick> | !gaccept | !gdecline | " +
	"!gleave | !gkick <nick> | !ginfo [name] | !gtop"

func dispatchRegister(src string, fields []string, g *Game, say, reply func(string)) {
	if len(fields) < 4 {
		reply("Usage: !register <nick> <class> <pass>")
		return
	}
	class := strings.Join(fields[2:len(fields)-1], " ")
	say(g.CmdRegister(src, fields[1], class, fields[len(fields)-1]))
}

func dispatchLogin(src string, fields []string, g *Game, reply func(string)) {
	if len(fields) < 2 {
		reply("Usage: !login <pass>")
		return
	}
	reply(g.CmdLogin(src, fields[1]))
}

func dispatchDualClass(src string, fields []string, g *Game, reply func(string)) {
	if len(fields) < 2 {
		reply("Usage: !dualclass <class>")
		return
	}
	reply(g.CmdDualClass(src, strings.Join(fields[1:], " ")))
}

func dispatchAlign(src string, fields []string, g *Game, reply func(string)) {
	if len(fields) < 2 {
		reply("Usage: !align <good|neutral|evil>")
		return
	}
	reply(g.CmdAlign(src, fields[1]))
}

func dispatchGuildCommand(src string, fields []string, g *Game, say, reply func(string)) {
	switch fields[0] {
	case "!gcreate":
		if len(fields) < 2 {
			reply("Usage: !gcreate <name>")
			return
		}
		say(g.CmdGCreate(src, strings.Join(fields[1:], " ")))
	case "!ginvite":
		if len(fields) < 2 {
			reply("Usage: !ginvite <nick>")
			return
		}
		reply(g.CmdGInvite(src, fields[1]))
	case "!gaccept":
		say(g.CmdGAccept(src))
	case "!gdecline":
		reply(g.CmdGDecline(src))
	case "!gleave":
		say(g.CmdGLeave(src))
	case "!gkick":
		if len(fields) < 2 {
			reply("Usage: !gkick <nick>")
			return
		}
		say(g.CmdGKick(src, fields[1]))
	case "!ginfo":
		reply(g.CmdGInfo(src, optArg(fields, 1)))
	case "!gtop":
		reply(g.CmdGTop())
	}
}

// optArg returns fields[i] if it exists, otherwise "".
func optArg(fields []string, i int) string {
	if i < len(fields) {
		return fields[i]
	}
	return ""
}
