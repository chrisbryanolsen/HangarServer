package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
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

type TTNMessage struct {
	app_id          string // Same as in the topic
	dev_id          string // Same as in the topic
	hardware_serial int64  // In case of LoRaWAN: the DevEUI
	port            int8   // LoRaWAN FPort
	counter         int8   // LoRaWAN frame counter
	is_retry        bool   // Is set to true if this message is a retry (you could also detect this from the counter)
	confirmed       bool   // Is set to true if this message was a confirmed message
}

//payload_raw byte[]         // Base64 encoded payload: [0x01, 0x02, 0x03, 0x04]

func main() {
	fmt.Println("Hello, world.")
	// Here we are instantiating the gorilla/mux router
	r := mux.NewRouter()

	// On the default page we will simply serve our static index page.
	r.Handle("/", http.FileServer(http.Dir("../views/")))

	// We will setup our server so we can serve static assest like images, css from the /static/{file} route
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("../static/"))))

	// Our application will run on port 9000. Here we declare the port and pass in our router.
	http.ListenAndServe(":9000", r)

	// Handle uplinks Posted from TTN from each Hangar Device
	r.Handle("/uplink/", ProcessUplink).Methods("POST")
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

	var m TTNMessage
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Fatal(err)
	}
	json.Unmarshal(body, &m)
})
