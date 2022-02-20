package main

import (
	"errors"
	"io"
	"log"
	"net"
)

func logError(err error) {
	log.Printf("[ERROR] %s\n", err)
}

func logInfo(msg string) {
	log.Printf("[INFO] %s\n", msg)
}

func writeAll(conn net.Conn, msg string) error {
	for len(msg) > 0 {
		n, err := conn.Write([]byte(msg))
		if err != nil {
			return err
		}
		msg = msg[n:]
	}

	return nil
}

type connection struct {
	conn net.Conn
	id   int
	buf  []byte
}

func (c *connection) logInfo(msg string) {
	log.Printf("[INFO] [%d: %s] %s\n", c.id, c.conn.RemoteAddr().String(), msg)
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
				line := string(c.buf[:i])
				c.buf = c.buf[i:]
				return line, nil
			}
		}
	}
}

func (c *connection) handle() {
	c.logInfo("Connection accepted")

	err := writeAll(c.conn, "220 mail.binutils.org ESMTP gomail\r\n")
	if err != nil {
		c.logError(err)
		return
	}

	for {
		line, err := c.readLine()
		if err == io.EOF {
			c.logError(errors.New("Unexpected EOF"))
			return
		}
		if err != nil {
			c.logError(err)
			return
		}

		// Should be EHLO/HELO?
		logInfo(line)
	}

	c.conn.Close()
	logInfo("Connection closed")
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
