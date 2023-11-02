package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
	"github.com/marf41/artnet"
)

var upgrader = newUpgrader()

//go:embed *.html *.css *.js
var webUI embed.FS

type connection struct {
	ws   *websocket.Conn
	send chan []byte
	h    *wshub
}

func (c *connection) run() {
	ticker := time.NewTicker(10 * time.Second)
	defer func() { c.ws.Close() }()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.ws.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			c.ws.WriteMessage(websocket.TextMessage, msg)
		case <-ticker.C:
			c.ws.WriteMessage(websocket.PingMessage, nil)
		}
	}
}

type wshub struct {
	connections map[*connection]bool
	broadcast   chan []byte
	register    chan *connection
	unregister  chan *connection
}

func newHub() *wshub {
	return &wshub{
		connections: make(map[*connection]bool),
		broadcast:   make(chan []byte),
		register:    make(chan *connection),
		unregister:  make(chan *connection),
	}
}

func (h *wshub) run() {
	for {
		select {
		case c := <-h.register:
			h.connections[c] = true
			log.Printf("Registered: %s (%d clients).\n", c.ws.Conn.RemoteAddr().String(), h.length())
		case c := <-h.unregister:
			if _, ok := h.connections[c]; ok {
				delete(h.connections, c)
				close(c.send)
				log.Printf("Unregistered: %s (%d clients).\n", c.ws.Conn.RemoteAddr().String(), h.length())
			}
		case m := <-h.broadcast:
			for c := range h.connections {
				select {
				case c.send <- m:
				default:
					h.unregister <- c
				}
			}
		}
	}
}

func (h *wshub) length() int {
	return len(h.connections)
}

func (h *wshub) unregisterWS(ws *websocket.Conn) {
	for conn := range h.connections {
		if conn.ws == ws {
			h.unregister <- conn
			return
		}
	}
}

type Settings struct {
	Universe    uint16 `json:"uni"`
	ChannelFrom int    `json:"ch"`
	Filter      string `json:"filter"`
	File        string `json:"file"`
}

var hub *wshub
var settings = Settings{ChannelFrom: 1}

func (s *Settings) save() {
	buf := new(bytes.Buffer)
	err := toml.NewEncoder(buf).Encode(s)
	if err == nil {
		log.Println(buf)
		os.WriteFile("svanth.toml", buf.Bytes(), 0644)
	}
}

func newUpgrader() *websocket.Upgrader {
	u := websocket.NewUpgrader()
	u.OnOpen(func(c *websocket.Conn) {
		// echo
		conn := &connection{c, make(chan []byte, 256), hub}
		conn.h.register <- conn
		log.Println("OnOpen:", c.RemoteAddr().String())
		go conn.run()
	})
	u.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		// echo
		log.Println("OnMessage:", messageType, string(data))
		// c.WriteMessage(messageType, data)
		if messageType != websocket.TextMessage {
			return
		}
		if strings.HasPrefix(string(data), "{") {
			var jsset Settings
			err := json.Unmarshal(data, &jsset)
			if err != nil {
				log.Println(err.Error())
				return
			}
			settings = jsset
			settings.save()
			return
		}
		if strings.Compare(string(data), "pdf") == 0 {
			pdfs, err := getPDFs()
			if err != nil {
				c.WriteMessage(websocket.TextMessage, []byte("{ \"error\": [ "+err.Error()+" ] }"))
				return
			}
			c.WriteMessage(websocket.TextMessage, []byte("{ \"pdfs\": [ "+strings.Join(pdfs, ", ")+" ] }"))
		}
		if strings.Compare(string(data), "set") == 0 {
			msg, err := json.Marshal(settings)
			if err == nil {
				c.WriteMessage(websocket.TextMessage, msg)
			}
			return
		}
	})
	u.OnClose(func(c *websocket.Conn, err error) {
		hub.unregisterWS(c)
		log.Println("OnClose:", c.RemoteAddr().String(), err)
	})
	return u
}

func onWebsocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		panic(err)
	}
	log.Println("Upgraded:", conn.RemoteAddr().String())
}

func art() {
	var b bytes.Buffer
	for {
		an, err := artnet.GetAndParse(false)
		if err != nil {
			log.Println(err)
		} else {
			if an.HasChannels && an.Port == settings.Universe {
				b.Reset()
				ch := an.ChannelsAsSlice(settings.ChannelFrom, 3)
				fmt.Fprintf(&b, "{ \"ch\": [ %d, %d, %d ] }", ch[0], ch[1], ch[2])
				hub.broadcast <- b.Bytes()
			}
		}
		time.Sleep(time.Second / 3)
	}
}

func getPDFs() ([]string, error) {
	pdfs := []string{}
	file, err := os.Open(".")
	if err != nil {
		return pdfs, err
	}
	defer file.Close()
	list, err := file.Readdirnames(0)
	if err != nil {
		return pdfs, err
	}
	for _, f := range list {
		if strings.HasSuffix(f, ".pdf") {
			pdfs = append(pdfs, "\""+f+"\"")
		}
	}
	return pdfs, nil
}

func hasPDF(file string) bool {
	pdfs, err := getPDFs()
	if err != nil {
		return false
	}
	for _, f := range pdfs {
		if f == ("\"" + file + "\"") {
			return true
		}
	}
	return false
}

func servePDF(w http.ResponseWriter, r *http.Request) {
	pdfs, err := getPDFs()
	if err != nil {
		http.Error(w, "Error reading directory", http.StatusInternalServerError)
		return
	}
	file := strings.TrimPrefix(r.RequestURI, "/pdf/")
	log.Printf("PDF requested: %s.", file)
	if hasPDF(file) {
		http.ServeFile(w, r, file)
		return
	}
	w.Write([]byte("[ " + strings.Join(pdfs, ", ") + " ]"))
}

func main() {
	hub = newHub()
	mux := &http.ServeMux{}
	mux.HandleFunc("/ws", onWebsocket)
	// mux.Handle("/", http.FileServer(http.FS(webUI)))
	mux.Handle("/", http.FileServer(http.Dir(".")))
	mux.HandleFunc("/pdf/", servePDF)
	engine := nbhttp.NewEngine(nbhttp.Config{
		Network:                 "tcp",
		Addrs:                   []string{"localhost:8080"},
		MaxLoad:                 1000000,
		ReleaseWebsocketPayload: true,
		Handler:                 mux,
	})

	_, err := toml.DecodeFile("svanth.toml", &settings)
	if err != nil {
		log.Printf("Error reading settings: %q.", err.Error())
	}

	err = engine.Start()
	if err != nil {
		log.Printf("nbio.Start failed: %v\n", err)
		return
	}

	go art()
	go hub.run()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	engine.Shutdown(ctx)
}
