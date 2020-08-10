package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/golang/gddo/httputil/header"
	"github.com/gorilla/mux"
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

func main() {
	fmt.Println("Hello, world.")
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

// ProcessUplink Decodes / unmarshals the JSON uplink object from TTN,
// See https://www.thethingsnetwork.org/docs/applications/http/ for a description of the JSON payload.
var ProcessUplink = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Process Uplink")

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

	body, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	fmt.Println(string(body))

	var m TTNMessage
	jsonError := json.Unmarshal(body, &m)
	if jsonError != nil {
		fmt.Printf("Unable to Parse JSON: %s\n", jsonError.Error)
		http.Error(w, jsonError.Error(), 500)
		return
	}

	fmt.Printf("Process Uplink From App: %s\n", m.AppID)
	fmt.Printf("Process Uplink From Device: %s\n", m.DevID)
	fmt.Printf("Serial: %v\n", m.HardwareSerial)
})
