package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	_ "github.com/mattn/go-sqlite3"

	"github.com/mdp/qrterminal/v3"
)

var client *whatsmeow.Client

func main() {
	dbLog := waLog.Stdout("Database", "DEBUG", true)
	// Make sure you add appropriate DB connector imports, e.g. github.com/mattn/go-sqlite3 for SQLite
	container, err := sqlstore.New("sqlite3", "file:examplestore.db?_foreign_keys=on", dbLog)
	if err != nil {
		panic(err)
	}
	// If you want multiple sessions, remember their JIDs and use .GetDevice(jid) or .GetAllDevices() instead.
	deviceStore, err := container.GetFirstDevice()
	if err != nil {
		panic(err)
	}

	clientLog := waLog.Stdout("Client", "DEBUG", true)
	client = whatsmeow.NewClient(deviceStore, clientLog)

	client.AddEventHandler(eventHandler)
	// download media and log messages

	if client.Store.ID == nil {
		// No ID stored, new login
		qrChan, _ := client.GetQRChannel(context.Background())
		err = client.Connect()
		if err != nil {
			panic(err)
		}
		for evt := range qrChan {
			if evt.Event == "code" {
				// Render the QR code here
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				// or just manually `echo 2@... | qrencode -t ansiutf8` in a terminal
				fmt.Println("QR code:", evt.Code)
			} else {
				fmt.Println("Login event:", evt.Event)
			}
		}
	} else {
		// Already logged in, just connect
		err = client.Connect()
		if err != nil {
			panic(err)
		}
	}

	// Listen to Ctrl+C (you can also do something else that prevents the program from exiting)
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	client.Disconnect()
}

func eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:

		toLog := strings.Builder{}

		// append time and sender to log
		toLog.WriteString(time.Now().Format(time.RFC822Z) + " ")
		toLog.WriteString(v.Info.Sender.String() + ": ")

		if v.IsViewOnce {
			toLog.WriteString("[ViewOnceMessage]\n")
		} else {

			downloaded, _ := client.DownloadAny(v.Message)

			// normal message, nothing to download
			if len(downloaded) == 0 {
				toLog.WriteString("'" + v.Message.GetConversation() + "'\n")
			} else {

				// store media
				var f *os.File
				var err2 error
				rand := strconv.Itoa(rand.Intn(100000000))

				if v.Message.AudioMessage != nil {
					f, err2 = os.OpenFile("voicemessage"+rand, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
					toLog.WriteString("[AudioMessage" + rand + "]\n")
				} else if v.Message.ImageMessage != nil {
					toLog.WriteString("[ImageMessage" + rand + "]\n")
					f, err2 = os.OpenFile("image"+rand+".jpeg", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
					toLog.WriteString("[DocumentMessage" + rand + "]\n")
				} else if v.Message.DocumentMessage != nil {
					f, err2 = os.OpenFile("document"+rand, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				} else {
					return
				}

				if err2 != nil {
					toLog.WriteString("Could not write downloaded to file: " + err2.Error() + "\n")
					return
				}
				if _, err := f.Write(downloaded); err != nil {
					toLog.WriteString("Could not write downloaded to file2: " + err.Error() + "\n")
					return
				}
				if err := f.Close(); err != nil {
					toLog.WriteString("Could not close file: " + err.Error() + "\n")
					return
				}
			}
		}

		fmt.Println("--------------")
		fmt.Println(toLog.String())
		fmt.Println("--------------")

		// log
		// If the file doesn't exist, create it, or append to the file
		logFile, err := os.OpenFile("messages.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Println("Could not write to log:")
			fmt.Println(err)
		}
		if _, err := logFile.Write([]byte(toLog.String())); err != nil {
			fmt.Println("Could not write to log2:")
			fmt.Println(err)
		}
		if err := logFile.Close(); err != nil {
			fmt.Println("Could not close log:")
			fmt.Println(err)
		}

	}
}
