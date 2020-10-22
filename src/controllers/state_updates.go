package controllers

import (
	"fmt"
	"github.com/SelaliAdobor/henchies-backend-go/src/models"
	"github.com/SelaliAdobor/henchies-backend-go/src/repository"
	"github.com/SelaliAdobor/henchies-backend-go/src/schema"
	websocketutil "github.com/SelaliAdobor/henchies-backend-go/src/websocketutils"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"net/http"
)

var websocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func (c *Controllers) GetStateUpdates(ctx *gin.Context) {
	var request schema.GetGameStateRequest
	if err := ctx.ShouldBindQuery(&request); err != nil {
		logrus.Error("failed to parse state updates request ", err)
		writeInvalidRequestResponse(ctx, err)
		return
	}
	playerKey := models.PlayerGameKey{Key: request.PlayerKey, OwnerIP: ctx.ClientIP()}
	playerStateChan, err := c.Repository.SubscribePlayerState(ctx, request.GameID, request.PlayerID, playerKey)

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

	gameStateChan, err := c.Repository.SubscribeGameState(ctx, request.GameID, request.PlayerID, models.PlayerGameKey{Key: request.PlayerKey, OwnerIP: ctx.ClientIP()})

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
	c.sendStateUpdates(playerStateChan, conn, gameStateChan)
}

func (c *Controllers) sendStateUpdates(playerStateChan chan models.PlayerState, conn *websocket.Conn, gameStateChan chan models.GameState) {
	isClosed := false
	var err error

	for {
		select {
		case player, ok := <-playerStateChan:
			if !ok {
				isClosed = true
				break
			}
			err = websocketutil.WriteValueToWebsocket(player.ToUpdate(), conn)
			if err != nil {
				isClosed = true
				break
			}
		case game, ok := <-gameStateChan:
			if !ok {
				isClosed = true
				break
			}
			err = websocketutil.WriteValueToWebsocket(game.ToUpdate(), conn)
			if err != nil {
				isClosed = true
				break
			}
		}
		if isClosed {
			logrus.Errorf("failed to write update to state update socket: %v", err)
			break
		}
	}
}