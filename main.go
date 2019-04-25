package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ahmdaeyz/messenger"
	"github.com/gocolly/colly"
	"github.com/google/go-cmp/cmp"
	"github.com/paked/configure"
	"github.com/pkg/errors"
	"github.com/valyala/fastjson"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// a messenger bot to download videos from facebook :D
type record struct {
	Time        time.Time
	RequiredURL string
}
type user struct {
	UserID  int64
	History []record
}

var (
	conf        = configure.New()
	verifyToken = conf.String("verify-token", "verify token", "The token used to verify facebook")
	verify      = conf.Bool("should-verify", false, "Whether or not the app should verify itself")
	pageToken   = conf.String("page-token", "page token", "The token that is used to verify the page on facebook")
	mongoURI    = conf.String("mongo-uri", "mongodb uri", "MongoDB URI")
	client      *messenger.Messenger
)

func determineListenAddress() (string, error) {
	port := os.Getenv("PORT")
	if port == "" {
		return "", fmt.Errorf("$PORT not set")
	}
	return ":" + port, nil
}

func init() {
	conf.Use(configure.NewFlag())
	conf.Use(configure.NewEnvironment())
	conf.Use(configure.NewJSONFromFile("./config.json"))
	c := colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/72.0.3626.109 Safari/537.36"),
	)
	ctx, _ = context.WithTimeout(context.Background(), 10*time.Second)
	db, err := mongo.Connect(ctx, options.Client().ApplyURI(*mongoURI))
	if err != nil {
		log.Fatal(err)
	}
	collection = db.Database("veo").Collection("users")
}

func main() {
	conf.Parse()
	client = messenger.New(messenger.Options{
		Verify:      *verify,
		VerifyToken: *verifyToken,
		Token:       *pageToken,
	})
	client.HandleMessage(messages)

	fmt.Println("Serving messenger bot on localhost:8080")
	listeningAt, err := determineListenAddress()
	if err != nil {
		log.Fatal(err)
	}
	http.ListenAndServe(listeningAt, client.Handler())
}
func messages(m messenger.Message, r *messenger.Response) {
	if len(m.Attachments) != 0 {
		user, err := updateUserRecord(m)
		if err != nil {
			log.Println(err)
		}
		if len(user.History) >= 2 {
			if cmp.Equal(user.History[len(user.History)-1], user.History[len(user.History)-2]) {
				err = r.SenderAction("mark_seen")
				if err != nil {
					log.Fatal("error sending sender action : ", err)
				}
				if len(user.History) >= 3 {
					dropFirstEntry := collection.FindOneAndUpdate(ctx, bson.M{"user_id": m.Sender.ID}, bson.M{"$pop": bson.M{"history": -1}})
					if dropFirstEntry.Err() != nil {
						log.Println("error droping first entry of user history", dropFirstEntry.Err())
					}
				}
			} else {
				videoLink, err := scrapHead(m)
				if err != nil {
					log.Println(err)
				}
				err = sendVidAttachment(r, videoLink)
				if err != nil {
					log.Println(err)
				}
			}
		} else {
			videoLink, err := scrapHead(m)
			if err != nil {
				log.Println(err)
			}
			err = sendVidAttachment(r, videoLink)
			if err != nil {
				log.Println(err)
			}
		}
	} else if strings.Contains(m.Text, "watch") || strings.Contains(m.Text, "videos") {
		user, err := updateUserRecord(m)
		if err != nil {
			log.Println(err)
		}
		if len(user.History) >= 2 {
			if cmp.Equal(user.History[len(user.History)-1], user.History[len(user.History)-2]) {
				err = r.SenderAction("mark_seen")
				if err != nil {
					log.Fatal("error sending sender action : ", err)
				}
				if len(user.History) >= 3 {
					dropFirstEntry := collection.FindOneAndUpdate(ctx, bson.M{"user_id": m.Sender.ID}, bson.M{"$pop": bson.M{"history": -1}})
					if dropFirstEntry.Err() != nil {
						log.Println("error droping first entry of user history", dropFirstEntry.Err())
					}
				}
			} else {
				videoLink, err := scrapMobileVidLink(m)
				if err != nil {
					log.Println(err)
				}
				err = sendVidAttachment(r, videoLink)
				if err != nil {
					log.Println(err)
				}
			}
		} else {
			videoLink, err := scrapMobileVidLink(m)
			if err != nil {
				log.Println(err)
			}
			err = sendVidAttachment(r, videoLink)
			if err != nil {
				log.Println(err)
			}
		}
	} else {
		r.Text(`Plz share the requested video with "send as message","send in messenger" or provide the video url`, messenger.ResponseType)
	}
}
func updateUserRecord(m messenger.Message) (*user, error) {
	user := &user{}
	ctx, _ = context.WithTimeout(context.Background(), 5*time.Second)
	update := collection.FindOneAndUpdate(ctx, bson.M{"user_id": m.Sender.ID}, bson.M{"$push": bson.M{"history": bson.M{"time": m.Time, "required_url": m.Attachments[len(m.Attachments)-1].URL}}})
	if update.Err() != nil {
		return nil, errors.Wrap(update.Err(), "error updating database")
	}
	if update.Decode(&user) != nil {
		res, err := collection.InsertOne(ctx, bson.M{"user_id": m.Sender.ID, "history": bson.A{bson.M{"time": m.Time, "required_url": m.Attachments[len(m.Attachments)-1].URL}}})
		if err != nil {
			return nil, errors.Wrap(err, "error inserting document")
		}
		_ = res
	}
	dbUser := collection.FindOne(ctx, bson.M{"user_id": m.Sender.ID})
	err = dbUser.Decode(&user)
	if err != nil {
		return nil, errors.Wrap(err, "error decoding : ")
	}
	return user, nil
}
func scrapMobileVidLink(m messenger.Message) (string, error) {
	var vidLink string
	c.OnHTML("._53mw", func(e *colly.HTMLElement) {
		value, err := fastjson.Parse(e.Attr("data-store"))
		if err != nil {
			return "", errors.Wrap(err, "error scraping (mobile method)")
		}
		videoLink = strings.Replace(strings.Replace(value.Get("src").String(), "\\", "", -1), "\"", "", -1)
	})
	c.Visit(strings.Replace(strings.TrimSpace(m.Text), "www", "m", -1))
	return vidLink, nil
}
func scrapHead(m messenger.Message) (string, error) {
	var vidLink string
	c.OnHTML("head > meta", func(e *colly.HTMLElement) {
		if e.Attr("property") == "og:video" {
			videoLink = e.Attr("content")
		}
	})
	err = c.Visit(m.Attachments[len(m.Attachments)-1].URL)
	if err != nil {
		return nil, errors.Wrap(err, "error scrapping video link : ")
	}
	return vidLink, nil
}
func sendVidAttachment(r *messenger.Response, videoLink string) error {
	if videoLink != "" {
		err = r.Attachment(messenger.VideoAttachment, videoLink, messenger.ResponseType)
		if err != nil {
			return errors.Wrap(err, "error sending attachment")
		}
	} else {
		r.Text("You might have sent an attachment that doesn't point to a video :'(", messenger.ResponseType)
	}
	return nil
}
