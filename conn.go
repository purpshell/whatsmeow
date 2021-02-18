//Package whatsapp provides a developer API to interact with the WhatsAppWeb-Servers.
package whatsapp

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "maunium.net/go/maulogger/v2"
)

type metric byte

const (
	debugLog metric = iota + 1
	queryResume
	queryReceipt
	queryMedia
	queryChat
	queryContacts
	queryMessages
	presence
	presenceSubscribe
	group
	read
	chat
	received
	pic
	status
	message
	queryActions
	block
	queryGroup
	queryPreview
	queryEmoji
	queryMessageInfo
	spam
	querySearch
	queryIdentity
	queryUrl
	profile
	contact
	queryVcard
	queryStatus
	queryStatusUpdate
	privacyStatus
	queryLiveLocations
	liveLocation
	queryVname
	queryLabels
	call
	queryCall
	queryQuickReplies
)

type flag byte

const (
	ignore flag = 1 << (7 - iota)
	ackRequest
	available
	notAvailable
	expires
	skipOffline
)

/*
Conn is created by NewConn. Interacting with the initialized Conn is the main way of interacting with our package.
It holds all necessary information to make the package work internally.
*/
type Conn struct {
	log log.Logger

	ws       *websocketWrapper
	listener *listenerWrapper

	connected bool
	loggedIn  bool

	session        *Session
	sessionLock    uint32
	handler        []Handler
	msgCount       int
	msgTimeout     time.Duration
	Store          *Store
	ServerLastSeen time.Time

	timeTag string // last 3 digits obtained after a successful login takeover

	longClientName  string
	shortClientName string
	clientVersion   string

	loginSessionLock sync.RWMutex
	Proxy            func(*http.Request) (*url.URL, error)

	writerLock sync.RWMutex
}

type Options struct {
	Proxy           func(*http.Request) (*url.URL, error)
	Timeout         time.Duration
	Handler         []Handler
	ShortClientName string
	LongClientName  string
	ClientVersion   string
	Store           *Store
	Log             log.Logger
}

func NewConn(opt *Options) *Conn {
	if opt == nil {
		panic(ErrOptionsNotProvided)
	}
	if opt.Log == nil {
		opt.Log = log.DefaultLogger
	}
	wac := &Conn{
		log:             opt.Log,
		handler:         make([]Handler, 0),
		msgCount:        0,
		msgTimeout:      opt.Timeout,
		Store:           newStore(),
		longClientName:  "github.com/Rhymen/go-whatsapp",
		shortClientName: "go-whatsapp",
		clientVersion:   "0.1.0",
	}
	if opt.Handler != nil {
		wac.handler = opt.Handler
	}
	if opt.Store != nil {
		wac.Store = opt.Store
	}
	if opt.Proxy != nil {
		wac.Proxy = opt.Proxy
	}
	if len(opt.ShortClientName) != 0 {
		wac.shortClientName = opt.ShortClientName
	}
	if len(opt.LongClientName) != 0 {
		wac.longClientName = opt.LongClientName
	}
	if len(opt.ClientVersion) != 0 {
		wac.clientVersion = opt.ClientVersion
	}
	return wac
}

func (wac *Conn) connect() (err error) {
	if wac.connected {
		return ErrAlreadyConnected
	}
	wac.connected = true
	defer func() { // set connected to false on error
		if err != nil {
			wac.connected = false
		}
	}()

	dialer := &websocket.Dialer{
		ReadBufferSize:   0,
		WriteBufferSize:  0,
		HandshakeTimeout: wac.msgTimeout,
		Proxy:            wac.Proxy,
	}

	headers := http.Header{"Origin": []string{"https://web.whatsapp.com"}}
	wac.log.Debugln("Dialing wss://web.whatsapp.com/ws")
	wsConn, _, err := dialer.Dial("wss://web.whatsapp.com/ws", headers)
	if err != nil {
		return fmt.Errorf("couldn't dial whatsapp web websocket: %w", err)
	}

	wsConn.SetCloseHandler(func(code int, text string) error {
		// from default CloseHandler
		message := websocket.FormatCloseMessage(code, "")
		err := wsConn.WriteControl(websocket.CloseMessage, message, time.Now().Add(time.Second))

		// our close handling
		_ = wac.Disconnect()
		wac.handle(&ErrConnectionClosed{Code: code, Text: text})
		return err
	})

	wac.ws = newWebsocketWrapper(wsConn)
	wac.listener = newListenerWrapper()

	wac.ws.Add(2)
	go wac.readPump()
	go wac.keepAlive(21000, 30000)

	wac.loggedIn = false
	return nil
}

func (wac *Conn) Disconnect() error {
	if !wac.connected {
		return ErrNotConnected
	}
	wac.log.Debugfln("Disconnecting websocket")
	wac.connected = false
	wac.loggedIn = false

	ws := wac.ws

	ws.cancel()
	ws.Wait()

	var err error
	if ws.conn != nil {
		err = ws.conn.Close()
	}
	wac.ws = nil
	wac.log.Debugfln("Websocket disconnection complete")

	return err
}

func (wac *Conn) IsLoginInProgress() bool {
	return wac.sessionLock == 1
}

func (wac *Conn) AdminTest() error {
	if !wac.connected {
		return ErrNotConnected
	}

	if !wac.loggedIn {
		return ErrNotLoggedIn
	}

	return wac.sendAdminTest()
}

// IsConnected returns whether the server connection is established or not
func (wac *Conn) IsConnected() bool {
	return wac.connected
}

//IsLoggedIn returns whether the you are logged in or not
func (wac *Conn) IsLoggedIn() bool {
	return wac.loggedIn
}
