package bitbot

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/whyrusleeping/hellabot"
	log "gopkg.in/inconshreveable/log15.v2"
)

//nolint gochecknoglobals
var (
	location   *time.Location
	timeFormat string
)

type reminderEvent struct {
	ID          int `gorm:"unique;AUTO_INCREMENT;PRIMARY_KEY"`
	Channel     string
	Author      string
	Description string
	People      string
	Time        time.Time
}

var ReminderTrigger = NamedTrigger{ //nolint:gochecknoglobals,golint
	ID:   "reminder",
	Help: "Set up events and remind them to concerned people. Usage: !remind list|time|add|remove|join|part",
	Condition: func(irc *hbot.Bot, m *hbot.Message) bool {
		return m.Command == "PRIVMSG" && strings.HasPrefix(m.Trailing, "!remind")
	},
	Action: func(irc *hbot.Bot, m *hbot.Message) bool {
		timeFormat = "2006-01-02 15:04"

		var err error
		location, err = time.LoadLocation("UTC")
		if err != nil {
			irc.Reply(m, "Something went wrong: Couldn't load timezone")
			log.Error("Reminder : Couldn't load UTC timezone", err.Error())
		}

		b.DB.AutoMigrate(&reminderEvent{})

		splitMSG := strings.Split(m.Content, " ")
		if len(splitMSG) < 2 {
			irc.Reply(m, "Not enough arguments provided")
			return true
		}

		switch splitMSG[1] {
		case "time":
			irc.Reply(m, getTime())
		case "add":
			irc.Reply(m, addEvent(m, irc))
		case "remove":
			irc.Reply(m, removeEvent(m))
		case "list":
			irc.Reply(m, listEvents(m, irc))
		case "join":
			irc.Reply(m, joinEvent(m))
		case "part":
			irc.Reply(m, partEvent(m))
		default:
			irc.Reply(m, "Wrong argument")
		}
		return true
	},
}

// This error is used when a badly formatted call of the trigger is made.
type wrongFormatError struct {
	arg string
}

func (e *wrongFormatError) Error() string {
	return fmt.Sprintf("%s : is not of the awaited !remind [command] [ID] format", e.arg)
}

func getMessageIDFromString(body string) (int, error) {
	// Parse message
	msg := strings.Split(body, " ")
	isAnID, err := regexp.MatchString("[0-9]+", msg[2])
	if err != nil {
		log.Info("Not and ID :", err)
	}

	if len(msg) != 3 || !isAnID {
		return -1, &wrongFormatError{body}
	}
	id, _ := strconv.Atoi(msg[2])
	return id, nil
}

// Signal yourself as interested in an event (Facebook™)
func joinEvent(message *hbot.Message) string {
	var event reminderEvent

	id, err := getMessageIDFromString(message.Content)
	if err != nil {
		return "Wrong command. format is : !remind join [ID]"
	}

	b.DB.Where("ID = ?", id).Take(&event)

	if strings.Contains(event.People, message.Name) {
		b.DB.Save(&event)
		return "You already subscribed to this event"
	}
	event.People = fmt.Sprintf("%s%s ", event.People, message.Name)
	b.DB.Save(&event)

	feedback := fmt.Sprintf("Added %s to \"%s\"",
		message.Name,
		event.Description)

	return feedback
}

func partEvent(message *hbot.Message) string {
	var event reminderEvent

	id, err := getMessageIDFromString(message.Content)
	if err != nil {
		return "Wrong command. format is : !remind part [ID]"
	}

	b.DB.Where("ID = ?", id).Take(&event)

	event.People = strings.Replace(event.People, message.Name+" ", "", -1)
	b.DB.Save(&event)

	feedback := fmt.Sprintf("Removed %s from \"%s\"",
		message.Name,
		event.Description)

	return feedback
}

// Remove an event given by his ID
func removeEvent(message *hbot.Message) string {
	var event reminderEvent

	id, err := getMessageIDFromString(message.Content)
	if err != nil {
		return "Wrong command. format is : !remind remove [ID]"
	}

	b.DB.Where("ID = ? AND Author = ?", id, message.Name).Take(&event)

	// Feedback Message construction
	var feedbackMessage string
	if event.ID == id {
		feedbackMessage = fmt.Sprintf("Deleted event %d : %s",
			event.ID,
			event.Description)

		// Delete
		b.DB.Delete(&event)
	} else {
		feedbackMessage = "No event you own with that ID"
	}

	return feedbackMessage
}

// Lists all the awaiting events in PM
func listEvents(message *hbot.Message, bot *hbot.Bot) string {
	// Get all the db rows, iterate through them, format them and send them to pm
	rows, err := b.DB.Model(&reminderEvent{}).Rows()
	if err != nil {
		log.Error("Reminder: Couldn't get DB rows", err)
	}

	var (
		event                   reminderEvent
		eventDescriptionMessage string
	)

	for rows.Next() {
		err := b.DB.ScanRows(rows, &event)
		if err != nil {
			log.Error("Reminder: Couldn't get a db row", err)
		}

		eventDescriptionMessage = fmt.Sprintf(
			"%d : [ %s ] at %s. reminderEvent author : %s, in channel %s, with %s",
			event.ID,
			event.Description,
			event.Time.Format(timeFormat),
			event.Author,
			event.Channel,
			event.People)
		bot.Msg(message.Name, eventDescriptionMessage)
	}

	return "I've PM'd you the list of awaiting events"
}

//FIXME : ...
func parseAddCommandMessage(body string) (string, string) {
	return "aaa", timeFormat
}

// Parses an event adding message and adds the event
func addEvent(message *hbot.Message, bot *hbot.Bot) string {
	// Parsing the message
	channel := message.To
	author := message.From

	description, datetime := parseAddCommandMessage(message.Content)

	// We take the two last parts of the message (with space as the separator)
	// and parse them as a time.
	timeOfreminderEvent, err := time.Parse(timeFormat, datetime)
	if err != nil {
		return fmt.Sprintf(
			"Couldn't parse request format is \"!remind add Jitsi Meeting %s\"",
			timeFormat)
	}

	// Adding it to the DB
	event := reminderEvent{
		Channel:     channel,
		Author:      author,
		Description: description,
		Time:        timeOfreminderEvent,
		People:      fmt.Sprintf("%s ", author)}
	b.DB.NewRecord(event)
	b.DB.Create(&event)

	// Launch a background routine that will HL interested people and clean the DB.
	timeToEvent := time.Until(event.Time) - time.Duration(2*time.Second)
	eventTimer := time.NewTimer(2 * time.Second)
	go func() {
		time.Sleep(timeToEvent)
		<-eventTimer.C

		var timerEvent reminderEvent

		b.DB.Where("Author = ? AND Description = ?",
			event.Author, event.Description).Find(&timerEvent)

		bot.Reply(message,
			fmt.Sprintf("%s : %s",
				timerEvent.Description,
				timerEvent.People))

		b.DB.Where("ID = ?", timerEvent.ID).Delete(reminderEvent{})
	}()

	// Feedback
	return fmt.Sprintf("Adding event \"%s\" by %s, at %s in %s",
		description,
		author,
		timeOfreminderEvent.Format(timeFormat),
		channel)
}

func getTime() string {
	now := time.Now().In(location)
	return now.Format(timeFormat)
}
