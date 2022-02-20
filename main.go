package main

import (
	"errors"
	"log"
	"net"
	"strconv"
	"strings"
)

func logError(err error) {
	log.Printf("[ERROR] %s\n", err)
}

func logInfo(msg string) {
	log.Printf("[INFO] %s\n", msg)
}

type message struct {
	clientDomain string
	smtpHeaders  map[string]string
	atmHeaders   map[string]string
	body         string
	from         string
	date         string
	subject      string
	to           string
}

type connection struct {
	conn net.Conn
	id   int
	buf  []byte
}

func (c *connection) logInfo(msg string, args ...interface{}) {
	args = append([]interface{}{c.id, c.conn.RemoteAddr().String()}, args...)
	log.Printf("[INFO] [%d: %s] "+msg+"\n", args...)
}

func (c *connection) logError(err error) {
	log.Printf("[ERROR] [%d: %s] %s\n", c.id, c.conn.RemoteAddr().String(), err)
}

func (c *connection) readLine() (string, error) {
	for {
		b := make([]byte, 1024)
		n, err := c.conn.Read(b)
		if err != nil {
			return "", err
		}

		c.buf = append(c.buf, b[:n]...)
		for i, b := range c.buf {
			// If end of line
			if b == '\n' && i > 0 && c.buf[i-1] == '\r' {
				// i-1 because drop the CRLF, no one cares after this
				line := string(c.buf[:i-1])
				c.buf = c.buf[i+1:]
				return line, nil
			}
		}
	}
}

func (c *connection) readMultiLine() (string, error) {
	for {
		noMoreReads := false
		for i, b := range c.buf {
			if i > 1 &&
				b != ' ' &&
				b != '\t' &&
				c.buf[i-2] == '\r' &&
				c.buf[i-1] == '\n' {
				// i-2 because drop the CRLF, no one cares after this
				line := string(c.buf[:i-2])
				c.buf = c.buf[i:]
				return line, nil
			}

			noMoreReads = c.isBodyClose(i)
		}

		if !noMoreReads {
			b := make([]byte, 1024)
			n, err := c.conn.Read(b)
			if err != nil {
				return "", err
			}

			c.buf = append(c.buf, b[:n]...)

			// If this gets here more than once it's going to be an infinite loop
		}
	}
}

func (c *connection) isBodyClose(i int) bool {
	return i > 4 &&
		c.buf[i-4] == '\r' &&
		c.buf[i-3] == '\n' &&
		c.buf[i-2] == '.' &&
		c.buf[i-1] == '\r' &&
		c.buf[i-0] == '\n'
}

func (c *connection) readToEndOfBody(toRead int) (string, error) {
	for {
		for i := range c.buf {
			if c.isBodyClose(i) {
				return string(c.buf[:i-4]), nil
			}
		}

		b := make([]byte, 1024)
		n, err := c.conn.Read(b)
		if err != nil {
			return "", err
		}

		c.buf = append(c.buf, b[:n]...)
	}
}

func (c *connection) writeLn(msg string) error {
	msg += "\r\n"
	for len(msg) > 0 {
		n, err := c.conn.Write([]byte(msg))
		if err != nil {
			return err
		}

		msg = msg[n:]
	}

	return nil
}

func (c *connection) handle() {
	defer c.conn.Close()
	c.logInfo("Connection accepted")

	err := c.writeLn("220 mail.binutils.org ESMTP gomail")
	if err != nil {
		c.logError(err)
		return
	}

	line, err := c.readLine()
	if err != nil {
		c.logError(err)
		return
	}

	msg := message{
		smtpHeaders: map[string]string{},
		atmHeaders:  map[string]string{},
	}
	if strings.HasPrefix(line, "EHLO") {
		msg.clientDomain = line[len("EHLO "):]
	} else {
		c.logError(errors.New("Expected EHLO got: " + line))
		return
	}

	c.logInfo("Received EHLO")

	err = c.writeLn("250-mail.binutils.org greets " + msg.clientDomain)
	if err != nil {
		c.logError(err)
		return
	}

	c.logInfo("Wrote EHLO greeting")

	ehloSettings := []string{
		"8BITMIME",
		"SIZE",
		"DSN",
	}
	for _, setting := range ehloSettings {
		err = c.writeLn("250-" + setting)
		if err != nil {
			c.logError(err)
			return
		}
	}

	c.logInfo("Wrote EHLO settings")

	err = c.writeLn("250 HELP")
	if err != nil {
		c.logError(err)
		return
	}

	c.logInfo("Done EHLO")

	var size int

	for line != "" {
		line, err = c.readLine()
		if err != nil {
			c.logError(err)
			return
		}

		pieces := strings.SplitN(line, ":", 2)
		smtpHeader := strings.ToUpper(pieces[0])

		// Special header without a value
		if smtpHeader == "DATA" {
			err = c.writeLn("354")
			if err != nil {
				c.logError(err)
				return
			}

			break
		}

		smtpValue := pieces[1]
		msg.smtpHeaders[smtpHeader] = smtpValue

		if smtpHeader == "MAIL FROM" {
			// e.g. MAIL FROM:<user@mail.com> SIZE=3000
			afterFrom := strings.SplitN(smtpValue, "> ", 2)[1]
			if strings.HasPrefix(afterFrom, "SIZE=") {
				afterEq := strings.SplitN(afterFrom, "SIZE=", 2)[1]
				size, err = strconv.Atoi(afterEq)
				if err != nil {
					c.logError(err)
					return
				}
			}
		}

		c.logInfo("Got header: " + line)

		err = c.writeLn("250 OK")
		if err != nil {
			c.logError(err)
			return
		}
	}

	c.logInfo("Done SMTP headers, reading ARPA text message headers")

	for {
		line, err = c.readMultiLine()
		if err != nil {
			c.logError(err)
			return
		}

		if strings.TrimSpace(line) == "" {
			break
		}

		pieces := strings.SplitN(line, ": ", 2)
		atmHeader := strings.ToUpper(pieces[0])
		atmValue := pieces[1]
		msg.atmHeaders[atmHeader] = atmValue

		if atmHeader == "SUBJECT" {
			msg.subject = atmValue
		}
		if atmHeader == "TO" {
			msg.to = atmValue
		}
		if atmHeader == "FROM" {
			msg.from = atmValue
		}
		if atmHeader == "DATE" {
			msg.date = atmValue
		}
	}

	c.logInfo("Done ARPA text message headers, reading body (%d bytes)", size)

	msg.body, err = c.readToEndOfBody(size)
	if err != nil {
		c.logError(err)
		return
	}

	c.logInfo("Got body (%d bytes)", len(msg.body))

	err = c.writeLn("250 OK")
	if err != nil {
		c.logError(err)
		return
	}

	c.logInfo("Message: %#v", msg)

	c.logInfo("Connection closed")
}

func main() {
	l, err := net.Listen("tcp", "0.0.0.0:25")
	if err != nil {
		panic(err)
	}
	// Close the listener when the application closes.
	defer l.Close()

	logInfo("Listening")

	id := 0
	for {
		conn, err := l.Accept()
		if err != nil {
			logError(err)
			continue
		}

		id += 1
		c := connection{conn, id, nil}
		go c.handle()
	}
}
