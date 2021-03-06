package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"time"

	"github.com/golang/gddo/httputil/header"
	"github.com/gorilla/mux"

	"github.com/gomodule/redigo/redis"
	"github.com/ugorji/go/codec"
)

/*
type TTNMessageMeta struct {
	time: 1970-01-01T00:00:00Z,   // Time when the server received the message
	frequency: 868.1,               // Frequency at which the message was sent
	modulation: LORA,             // Modulation that was used - LORA or FSK
	data_rate: SF7BW125,          // Data rate that was used - if LORA modulation
	bit_rate: 50000,                // Bit rate that was used - if FSK modulation
	coding_rate: 4/5,             // Coding rate that was used
	gateways: [
	{
		gtw_id: ttn-herengracht-ams, // EUI of the gateway
		timestamp: 12345,              // Timestamp when the gateway received the message
		time: 1970-01-01T00:00:00Z,  // Time when the gateway received the message - left out when gateway does not have synchronized time
		channel: 0,                    // Channel where the gateway received the message
		rssi: -25,                     // Signal strength of the received message
		snr: 5,                        // Signal to noise ratio of the received message
		rf_chain: 0,                   // RF chain where the gateway received the message
		latitude: 52.1234,             // Latitude of the gateway reported in its status updates
		longitude: 6.1234,             // Longitude of the gateway
		altitude: 6                    // Altitude of the gateway
	},
	//...more if received by more gateways...
	],
	latitude: 52.2345,              // Latitude of the device
	longitude: 6.2345,              // Longitude of the device
	altitude: 2                     // Altitude of the device
}
*/

// TTNMessage is the format of the message sent from The Things Network uplink callback
type TTNMessage struct {
	AppID          string `json:"app_id"`          // Same as in the topic
	DevID          string `json:"dev_id"`          // Device ID
	HardwareSerial string `json:"hardware_serial"` // In case of LoRaWAN: the DevEUI
	Port           int16  `json:"port"`            // LoRaWAN FPort
	Counter        int16  `json:"counter"`         // LoRaWAN frame counter
	IsRetry        bool   `json:"isRetry"`         // Is set to true if this message is a retry (you could also detect this from the counter)
	Confirmed      bool   `json:"confirmed"`       // Is set to true if this message was a confirmed message
	PayloadRaw     []byte `json:"payload_raw"`     // Base64 encoded payload: [0x01, 0x02, 0x03, 0x04]
	DownlinkURL    string `json:"downlink_url"`
}

// ClientMsg is the Command / status update message sent from clients
type ClientMsg struct {
	DevID      string // Device ID
	MsgTime    int64  // The Time the server recived the message (persisted in Redis)
	Cmd        string `codec:"cmd"`
	MyTime     int64  `codec:"my-time"`
	PowerState []bool `codec:"state"`
}

// ClientResp is sent as a downlink to client devices to configure / setup that device
type ClientResp struct {
	Cmd     string     `codec:"cmd"`
	CurTime int64      `codec:"cur-time"` // Current UTC time from the server, used to set device time
	Sched   []Schedule `codec:"cmd-data"` // Data for the command
}

// Schedule defines a scheduled power change event
type Schedule struct {
	St  bool   `codec:"st"`  // Power State True = ON
	Dow string `codec:"dow"` // Day Of Week for this schedule 0 = Mon, 6 = Sun
	Tm  string `codec:"tm"`  // Time in UTC '1530' when the scheduled power state should change
}

// DownlinkMsg contains a message to be sent back to TTN and be downloaded by the device
type DownlinkMsg struct {
	DevID      string `json:"dev_id"`      // Device ID
	Port       int16  `json:"port"`        // LoRaWAN FPort
	Confirmed  bool   `json:"confirmed"`   // Is set to true if this message was a confirmed message
	PayloadRaw []byte `json:"payload_raw"` // Base64 encoded payload: [0x01, 0x02, 0x03, 0x04]
}

// Redis data connection pooling
var (
	pool *redis.Pool
)

func main() {
	fmt.Println("Hangar Server Startup.")

	// Create the Redis Connection Pool
	pool = newPool()

	// Here we are instantiating the gorilla/mux router
	r := mux.NewRouter()

	// On the default page we will simply serve our static index page.
	r.Handle("/", http.FileServer(http.Dir("../views/")))

	// Handle uplinks Posted from TTN from each Hangar Device
	r.Handle("/uplink/", ProcessUplink).Methods("POST")

	// We will setup our server so we can serve static assest like images, css from the /static/{file} route
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("../static/"))))

	// Our application will run on port 9000. Here we declare the port and pass in our router.
	http.ListenAndServe(":9000", r)
}

// newPool configures the connection pool to our redis cache persisted store
func newPool() *redis.Pool {
	return &redis.Pool{
		// Maximum number of idle connections in the pool.
		MaxIdle: 10,
		// max number of connections
		MaxActive:   25,
		IdleTimeout: 300 * time.Second,
		Wait:        true,

		// Test the connection to make sure it is still viable
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return err
		},

		// Dial is an application supplied function for creating and
		// configuring a connection.
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", "192.168.0.15:6379")
			if err != nil {
				panic(err.Error())
			}
			return c, err
		},
	}
}

// saveStatus saves the status received from a device in the redis cache. If have an expire time set on
// the record so status records do not persist forever.
func saveStatus(clientMsg *ClientMsg) error {
	conn := pool.Get()
	defer conn.Close()

	// Marshal the status data into JSON to store in the cache
	json, err := json.Marshal(clientMsg)
	if err != nil {
		return err
	}
	s := string(json)

	// Create the Key for the status record
	tm := strconv.FormatUint(uint64(clientMsg.MsgTime), 10)
	key := "STAT:" + clientMsg.DevID + ":" + tm
	fmt.Println("Status Key: " + key)
	fmt.Println("Status JSON: " + s)

	conn.Send("MULTI")
	conn.Send("SET", key, s)
	conn.Send("EXPIRE", key, 86400*7) // Keep Status records 7 days
	_, err = conn.Do("EXEC")
	if err != nil {
		return err
	}
	return nil
}

// getSched reads the current device schedule from the redis cache
func getSched(clientMsg *ClientMsg) (sched []Schedule, err error) {
	conn := pool.Get()
	defer conn.Close()

	// Build the Key for the SCHED record
	key := "SCHED:" + clientMsg.DevID
	fmt.Println("Sched Key: " + key)

	rslt, err := redis.Bytes(conn.Do("GET", key))
	if err != nil {
		return nil, fmt.Errorf("Unable to Query Redis: %s, %w", err.Error(), err)
	}

	fmt.Println("Sched Json: " + string(rslt))
	err = json.Unmarshal(rslt, &sched)
	if err != nil {
		return nil, fmt.Errorf("Unable to Parse JSON Redis Result: %s, %w", err.Error(), err)
	}

	return sched, err
}

// decodeUplink will unmarshal the JSON data recived into a struct and also decode the MessagePack payload
// that is sent from the client device and should include a client command request.
func decodeUplink(r *http.Request) (msg TTNMessage, clientMsg ClientMsg, err error) {
	body, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		return msg, clientMsg, fmt.Errorf("Unable to Read Body: %s, %w", err.Error(), err)
	}
	//fmt.Println(string(body))

	err = json.Unmarshal(body, &msg)
	if err != nil {
		return msg, clientMsg, fmt.Errorf("Unable to Parse JSON: %s, %w", err.Error(), err)
	}

	var mh codec.Handle = new(codec.MsgpackHandle)
	var dec *codec.Decoder = codec.NewDecoderBytes(msg.PayloadRaw, mh)
	err = dec.Decode(&clientMsg)
	if err != nil {
		return msg, clientMsg, fmt.Errorf("Unable to Decode MessagePack: %s, %w", err.Error(), err)
	}

	// Update the current server time into the message for later use
	now := time.Now()
	clientMsg.MsgTime = now.Unix()
	clientMsg.DevID = msg.DevID

	fmt.Printf("Process Uplink From App: %s\n", msg.AppID)
	fmt.Printf("Process Uplink From Device: %s\n", msg.DevID)
	fmt.Printf("Serial: %v\n", msg.HardwareSerial)

	return msg, clientMsg, err
}

// startupRequest will process the startup request message from a client and send back the current time and
// any power schedule that has been defined for this device
func startupRequest(ttnMsg *TTNMessage, clientMsg *ClientMsg) error {
	fmt.Printf("Send Client Startup Response: %s\n", ttnMsg.DevID)
	fmt.Printf("Send Response To: %s\n", ttnMsg.DownlinkURL)

	// Query Redis Cache for the schedule information
	sched, err := getSched(clientMsg)
	if err != nil {
		return fmt.Errorf("Unable to Retrive device schedule: %s, %w", err.Error(), err)
	}

	now := time.Now()
	clientResp := ClientResp{"init", now.Unix(), sched}

	var mh codec.Handle = new(codec.MsgpackHandle)
	var msgResp []byte
	var dec *codec.Encoder = codec.NewEncoderBytes(&msgResp, mh)
	encError := dec.Encode(&clientResp)
	if encError != nil {
		return fmt.Errorf("Unable to Encode Client Response Message: %s, %w", encError.Error(), encError)
	}

	downlinkMsg := DownlinkMsg{ttnMsg.DevID, ttnMsg.Port, false, msgResp}
	jsonReq, jsonError := json.Marshal(&downlinkMsg)
	if jsonError != nil {
		return fmt.Errorf("Unable to Json Encode Downlink Message: %s, %w", jsonError.Error(), jsonError)
	}

	resp, err := http.Post(ttnMsg.DownlinkURL, "application/json", bytes.NewBuffer(jsonReq))
	if err != nil {
		return fmt.Errorf("Unable to Send DownLink Request: %s, %w", err.Error(), err)
	}
	defer resp.Body.Close()

	fmt.Println("Response Status:", resp.Status)
	fmt.Println("Response Headers:", resp.Header)
	body, _ := ioutil.ReadAll(resp.Body)
	fmt.Println("Response Body:", string(body))

	return nil
}

// ProcessUplink Decodes / unmarshals the JSON uplink object from TTN,
// See https://www.thethingsnetwork.org/docs/applications/http/ for a description of the JSON payload.
var ProcessUplink = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	fmt.Println("\nProcess Uplink")

	// If the Content-Type header is present, check that it has the value
	// application/json. Note that we are using the gddo/httputil/header
	// package to parse and extract the value here, so the check works
	// even if the client includes additional charset or boundary
	// information in the header.
	if r.Header.Get("Content-Type") != "" {
		value, _ := header.ParseValueAndParams(r.Header, "Content-Type")
		if value != "application/json" {
			msg := "Content-Type header is not application/json"
			http.Error(w, msg, http.StatusUnsupportedMediaType)
			return
		}
	}

	// Unmarshal the JSON data and pull out the client command that was sent
	ttnMsg, clientMsg, err := decodeUplink(r)
	if err != nil {
		fmt.Printf("Unable to Parse / Decode JSON: %s", err.Error())
		msg := "Invalid JSON or MessagePack payload"
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	fmt.Printf("Client Command: %v\n", clientMsg.Cmd)
	fmt.Printf("Client Time: %v\n", clientMsg.MyTime)
	if len(clientMsg.PowerState) == 2 {
		fmt.Printf("Power Port 1 State: %v\n", clientMsg.PowerState[0])
		fmt.Printf("Power Port 2 State: %v\n", clientMsg.PowerState[1])
	}

	if clientMsg.Cmd == "start" {
		startupRequest(&ttnMsg, &clientMsg)
	} else if clientMsg.Cmd == "status" {
		err = saveStatus(&clientMsg)
		if err != nil {
			fmt.Printf("Unable Persist Status Message: %s", err.Error())
		}
	}
})
