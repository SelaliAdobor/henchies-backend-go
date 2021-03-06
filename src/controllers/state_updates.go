package controllers

import (
	"errors"
	"fmt"
	"github.com/SelaliAdobor/henchies-backend-go/src/models"
	"github.com/SelaliAdobor/henchies-backend-go/src/repository"
	"github.com/SelaliAdobor/henchies-backend-go/src/schema"
	"github.com/SelaliAdobor/henchies-backend-go/src/websocketutil"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"net/http"
	"time"
)

var websocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

const pingInterval = 30 * time.Second
const maxInactiveInterval = 5 * time.Minute

// GetStateUpdates creates a WebSocket connection that will
func (c *Controllers) GetStateUpdates(ctx *gin.Context) {
	var request schema.GetGameStateRequest
	if err := ctx.ShouldBindQuery(&request); err != nil {
		logrus.Error("failed to parse state updates request ", err)
		writeInvalidRequestResponse(ctx, err)
		return
	}
	playerKey := models.PlayerGameKey{Key: request.PlayerKey, OwnerIP: ctx.ClientIP()}
	playerFinished, playerStateChan, err := c.Repository.SubscribePlayerState(ctx, request.GameID, request.PlayerID, playerKey)

	if err != nil {
		if err == repository.UnauthorizedPlayer {
			logrus.Error("unauthorized player attempted to subscribe to state", err)
			writeAuthenticationErrorResponse(ctx, err)
		} else {
			logrus.Error("failed to subscribe to player state", err)
			writeInternalErrorResponse(ctx, err)
		}
		return
	}

	gameFinished, gameStateChan, err := c.Repository.SubscribeGameState(ctx, request.GameID, request.PlayerID, models.PlayerGameKey{Key: request.PlayerKey, OwnerIP: ctx.ClientIP()})

	if err != nil {
		logrus.Error("failed to subscribe to game state", err)
		writeInternalErrorResponse(ctx, err)
		return
	}

	conn, err := websocketUpgrader.Upgrade(ctx.Writer, ctx.Request, nil)
	if err != nil {
		writeInternalErrorResponse(ctx, fmt.Errorf("failed to upgrade websocket: %w", err))
		return
	}
	c.sendStateUpdates(playerStateChan, conn, gameStateChan, gameFinished, playerFinished)
}

func (c *Controllers) sendStateUpdates(playerStateChan chan models.PlayerState, conn *websocket.Conn, gameStateChan chan models.GameState, gameFinished chan struct{}, playerFinished chan struct{}) {
	isClosed := false
	var err error

	defer close(playerFinished)
	defer close(gameFinished)

	pingTicker := time.NewTicker(pingInterval)
	inactivityTimer := time.NewTimer(maxInactiveInterval)

	resetInactivity := func() {
		if !inactivityTimer.Stop() {
			<-inactivityTimer.C
		}
		inactivityTimer.Reset(maxInactiveInterval)
	}

	for {
		select {
		case playerState, ok := <-playerStateChan:
			if err = writeStateUpdate(conn, playerState, ok); err != nil {
				isClosed = true
				break
			}
			resetInactivity()
		case gameState, ok := <-gameStateChan:
			if err = writeStateUpdate(conn, gameState, ok); err != nil {
				isClosed = true
				break
			}
			resetInactivity()
		case _, ok := <-pingTicker.C:
			if err = writeStateUpdate(conn, models.PingUpdate, ok); err != nil {
				isClosed = true
				break
			}
		case <-inactivityTimer.C:
			isClosed = true
			err = errors.New("inactivity timer triggered")
		}
		if isClosed {
			logrus.Errorf("failed to write update to state update socket: %v", err)
			break
		}
	}
}

func writeStateUpdate(conn *websocket.Conn, value models.StateUpdateField, ok bool) error {
	if !ok {
		return errors.New("channel closed")
	}

	err := websocketutil.WriteValueToWebsocket(value.ToUpdate(), conn)
	if err != nil {
		return fmt.Errorf("error writing to websocket: %w", err)
	}
	return nil
}
