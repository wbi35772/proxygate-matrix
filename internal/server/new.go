package server

import (
	"log"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/matrix"
	"github.com/ExpTechTW/proxygate/internal/service"
	"github.com/ExpTechTW/proxygate/internal/service/speedtest"
	"github.com/ExpTechTW/proxygate/internal/store"
	"github.com/ExpTechTW/proxygate/internal/vpn"
	"github.com/ExpTechTW/proxygate/internal/vpngate"
)

func New(
	configStore *config.Store,
	database *store.Store,
	manager *vpn.Manager,
	matrixManager *matrix.Manager,
	refresher *vpngate.Refresher,
	speedTests *speedtest.Service,
	services []service.Service,
	restart chan<- struct{},
	logger *log.Logger,
) *Server {
	return &Server{
		config: configStore, database: database, manager: manager, matrix: matrixManager,
		refresher: refresher, speedTests: speedTests,
		services: services, restart: restart, logger: logger,
	}
}
