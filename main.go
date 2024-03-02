package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
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

// TODO: Test all kind of messages and what is important to log, exampld: edits

var (
	client        *whatsmeow.Client
	MessageLogger *log.Logger
	MediaLogger   *log.Logger
	ErrorLogger   *log.Logger
)

func main() {

	// make sure folder exist to store media
	if err := ensureFolder("./media"); err != nil {
		log.Fatal(err)
	}

	// set up my own message log
	file, err := os.OpenFile("message_log.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		log.Fatal(err)
	}
	MessageLogger = log.New(file, "MESSAGE: ", log.Lmsgprefix)
	MediaLogger = log.New(file, "MEDIA: ", log.Lmsgprefix)
	ErrorLogger = log.New(file, "ERROR: ", log.Ldate|log.Ltime)

	dbLog := waLog.Stdout("Database", "DEBUG", true)

	// Make sure you add appropriate DB connector imports, e.g. github.com/mattn/go-sqlite3 for SQLite
	container, err := sqlstore.New("sqlite3", "file:storage.db?_foreign_keys=on", dbLog)
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

	client.AddEventHandler(messageHandler)
	// download media and log messages

	// check if logged in before
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

	// Listen to Ctrl+C to prevent program from exiting
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	client.Disconnect()
}

func fileExtensionFromMimeType(mimeType string) string {

	split := strings.Split(mimeType, "/")

	if len(split) < 2 {
		return ".unknown"
	}

	return "." + split[1]
}

func ensureFolder(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err := os.Mkdir(path, 0777)

		return err
	} else {
		return err
	}
}

func isOlderThanOneHour(timestamp time.Time) bool {
	// Get the current time
	currentTime := time.Now()

	// Calculate the duration between the current time and the provided timestamp
	duration := currentTime.Sub(timestamp)

	// Check if the duration is greater than one hour
	return duration > time.Hour
}

func storeMedia(fileName string, mediaContent []byte) {
	f, err := os.OpenFile(filepath.Join("./media", filepath.Base(fileName)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

	if err != nil {
		ErrorLogger.Println("Could open file '" + fileName + "': " + err.Error())
		return
	}
	if _, err := f.Write(mediaContent); err != nil {
		ErrorLogger.Println("Could write to file '" + fileName + "': " + err.Error())
	}
	if err := f.Close(); err != nil {
		ErrorLogger.Println("Could not close file '" + fileName + "': " + err.Error())
		return
	}
}

func isFolderSizeGreaterThanXGB(folderPath string, x float64) (bool, error) {
	var folderSize int64

	err := filepath.Walk(folderPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			folderSize += info.Size()
		}
		return nil
	})

	if err != nil {
		return false, err
	}

	// Convert to gigabytes
	const GB = 1 << 30
	sizeInGB := float64(folderSize) / float64(GB)

	return sizeInGB > x, nil
}

func isFileSizeGreaterThanXGB(filePath string, x float64) (bool, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return false, err
	}

	// Get file size in bytes
	fileSize := fileInfo.Size()

	// Convert to gigabytes
	const GB = 1 << 30
	sizeInGB := float64(fileSize) / float64(GB)

	return sizeInGB > x, nil
}

func deleteFolder(folderPath string) error {
	err := os.RemoveAll(folderPath)
	if err != nil {
		return err
	}
	return nil
}

func truncateFile(filePath string) error {
	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_TRUNC, os.ModePerm)
	if err != nil {
		return err
	}
	defer file.Close()

	return nil
}

// check size of media folder and message_log and reset them if they are too big
func resetStorageIfTooBig() {

	folderTooBig, err := isFolderSizeGreaterThanXGB("./media", 10)

	if err != nil {
		ErrorLogger.Println(err)
		return
	}

	if folderTooBig {
		deleteFolder("./media")
		ensureFolder("./media")
	}

	logTooBig, err := isFileSizeGreaterThanXGB("message_log.txt", 10)

	if err != nil {
		ErrorLogger.Println(err)
		return
	}

	if logTooBig {
		truncateFile("message_log.txt")
	}
}

func messageHandler(evt interface{}) {

	resetStorageIfTooBig()

	// switch with only one case to only handle messages in this handler function
	switch v := evt.(type) {
	case *events.Message:

		// only store things that are not older than one hour when program is started
		if isOlderThanOneHour(v.Info.Timestamp) {
			return
		}

		logMessage := strings.Builder{}

		// append time and sender to log
		// example: "08 Mar 22 15:53 +0100 491794******@s.whatsapp.net: "
		logMessage.WriteString(v.Info.Sender.String() + ": ")

		mediaContent, _ := client.DownloadAny(v.Message)

		// media content is available and should be stored
		if len(mediaContent) > 0 {

			// generate name for media
			var fileName string

			now_time := time.Now().Format(time.DateTime)

			if v.Message.AudioMessage != nil {
				fileName = "Audio_" + now_time + fileExtensionFromMimeType(v.Message.AudioMessage.GetMimetype())

			} else if v.Message.ImageMessage != nil {
				fileName = "Image_" + now_time + fileExtensionFromMimeType(v.Message.ImageMessage.GetMimetype())

			} else if v.Message.VideoMessage != nil {
				fileName = "Video_" + now_time + fileExtensionFromMimeType(v.Message.VideoMessage.GetMimetype())

			} else if v.Message.DocumentMessage != nil {
				fileName = "Document_" + now_time + fileExtensionFromMimeType(v.Message.DocumentMessage.GetMimetype())

			} else {
				fileName = "Other_" + now_time + ".unknown"
			}

			b, _ := json.MarshalIndent(v, "", "  ")
			MediaLogger.Printf(string(b))
			storeMedia(fileName, mediaContent)
		} else {
			b, _ := json.MarshalIndent(v, "", "  ")
			MessageLogger.Printf(string(b))
		}
	}
}
