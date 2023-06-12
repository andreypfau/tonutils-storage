package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/xssnick/tonutils-go/adnl"
	"github.com/xssnick/tonutils-go/adnl/dht"
	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-storage/config"
	"github.com/xssnick/tonutils-storage/db"
	"github.com/xssnick/tonutils-storage/storage"
	"log"
	"math/bits"
	"net"
	"os"
	"strings"
)

var (
	DBPath    = flag.String("db", "", "Path to db folder")
	Verbosity = flag.Int("debug", 0, "Debug logs")
)

var Storage *db.Storage
var Connector storage.NetConnector

func main() {
	flag.Parse()

	storage.FullDownload = true
	storage.Logger = func(v ...any) {}
	adnl.Logger = func(v ...any) {}
	dht.Logger = func(v ...any) {}

	switch *Verbosity {
	case 3:
		adnl.Logger = log.Println
		fallthrough
	case 2:
		dht.Logger = log.Println
		fallthrough
	case 1:
		storage.Logger = log.Println

	}

	_ = pterm.DefaultBigText.WithLetters(
		putils.LettersFromStringWithStyle("Ton", pterm.FgBlue.ToStyle()),
		putils.LettersFromStringWithStyle("Utils", pterm.FgLightBlue.ToStyle())).
		Render()

	pterm.DefaultBox.WithBoxStyle(pterm.NewStyle(pterm.FgLightBlue)).Println(pterm.LightWhite("   Storage   "))

	if *DBPath == "" {
		pterm.Error.Println("DB path should be specified with -db flag")
		os.Exit(1)
	}

	cfg, err := config.LoadConfig(*DBPath)
	if err != nil {
		pterm.Error.Println("Failed to load config:", err.Error())
		os.Exit(1)
	}

	ldb, err := leveldb.OpenFile(*DBPath+"/db", nil)
	if err != nil {
		pterm.Error.Println("Failed to load db:", err.Error())
		os.Exit(1)
	}

	var ip net.IP
	if cfg.ExternalIP != "" {
		ip = net.ParseIP(cfg.ExternalIP)
		if ip == nil {
			pterm.Error.Println("External ip is invalid")
			os.Exit(1)
		}
	}

	lsCfg, err := liteclient.GetConfigFromUrl(context.Background(), "https://ton-blockchain.github.io/global.config.json")
	if err != nil {
		pterm.Error.Println("Failed to download ton config:", err.Error())
		os.Exit(1)
	}

	gate := adnl.NewGateway(cfg.Key)

	serverMode := ip != nil
	if serverMode {
		gate.SetExternalIP(ip)
		err = gate.StartServer(cfg.ListenAddr)
		if err != nil {
			pterm.Error.Println("Failed to start adnl gateway in server mode:", err.Error())
			os.Exit(1)
		}
	} else {
		err = gate.StartClient()
		if err != nil {
			pterm.Error.Println("Failed to start adnl gateway in client mode:", err.Error())
			os.Exit(1)
		}
	}

	dhtGate := adnl.NewGateway(cfg.Key)
	if err = dhtGate.StartClient(); err != nil {
		pterm.Error.Println("Failed to init dht adnl gateway:", err.Error())
		os.Exit(1)
	}

	dhtClient, err := dht.NewClientFromConfig(dhtGate, lsCfg)
	if err != nil {
		pterm.Error.Println("Failed to init dht client:", err.Error())
		os.Exit(1)
	}

	downloadGate := adnl.NewGateway(cfg.Key)
	if err = downloadGate.StartClient(); err != nil {
		pterm.Error.Println("Failed to init dht downloader gateway:", err.Error())
		os.Exit(1)
	}

	srv := storage.NewServer(dhtClient, gate, cfg.Key, serverMode)
	Connector = storage.NewConnector(srv)

	Storage, err = db.NewStorage(ldb, Connector, true)
	if err != nil {
		pterm.Error.Println("Failed to init storage:", err.Error())
		os.Exit(1)
	}
	srv.SetStorage(Storage)

	pterm.Info.Println("If you use it for commercial purposes please consider", pterm.LightWhite("donation")+". It allows us to develop such products 100% free.")
	pterm.Info.Println("We also have telegram group if you have some questions.", pterm.LightBlue("https://t.me/tonrh"))

	pterm.Success.Println("Storage started, server mode:", serverMode)
	list()

	for {
		cmd, err := pterm.DefaultInteractiveTextInput.Show("Command:")
		if err != nil {
			panic(err)
		}

		parts := strings.Split(cmd, " ")
		if len(parts) == 0 {
			continue
		}

		switch parts[0] {
		case "download":
			if len(parts) < 2 {
				pterm.Error.Println("Usage: download [bag_id]")
				continue
			}
			download(parts[1])
		case "create":
			if len(parts) < 3 {
				pterm.Error.Println("Usage: create [path] [description]")
				continue
			}
			create(parts[1], parts[2])
		case "list":
			list()
		default:
			fallthrough
		case "help":
			pterm.Info.Println("Commands:\n"+
				"create [path] [description]\n",
				"download [bag_id]\n",
				"help\n",
			)
		}
	}
}

func download(bagId string) {
	bag, err := hex.DecodeString(bagId)
	if err != nil {
		pterm.Error.Println("Invalid bag id:", err.Error())
		return
	}

	if len(bag) != 32 {
		pterm.Error.Println("Invalid bag id: should be 32 bytes hex")
		return
	}

	tor := Storage.GetTorrent(bag)
	if tor == nil {
		tor = storage.NewTorrent(*DBPath+"/downloads/"+bagId, Storage, Connector)
		tor.BagID = bag

		if err = tor.Start(true); err != nil {
			pterm.Error.Println("Failed to start:", err.Error())
			return
		}

		err = Storage.SetTorrent(tor)
		if err != nil {
			pterm.Error.Println("Failed to set storage:", err.Error())
			os.Exit(1)
		}
	} else {
		if err = tor.Start(true); err != nil {
			pterm.Error.Println("Failed to start:", err.Error())
			return
		}
	}

	pterm.Success.Println("Bag added")
}

func create(path, name string) {
	it, err := storage.CreateTorrent(path, name, Storage, Connector)
	if err != nil {
		pterm.Error.Println("Failed to create bag:", err.Error())
		return
	}
	it.Start(true)

	err = Storage.SetTorrent(it)
	if err != nil {
		pterm.Error.Println("Failed to add bag:", err.Error())
		return
	}

	pterm.Success.Println("Bag created and ready:", pterm.Cyan(hex.EncodeToString(it.BagID)))
	list()
}

func list() {
	var table = pterm.TableData{
		{"Bag ID", "Description", "Downloaded", "Size", "Peers", "Download", "Upload", "Completed"},
	}

	for _, t := range Storage.GetAll() {
		if t.Info == nil {
			continue
		}
		mask := t.PiecesMask()
		downloadedPieces := 0
		for _, b := range mask {
			downloadedPieces += bits.OnesCount8(b)
		}
		full := t.Info.FileSize - t.Info.HeaderSize
		downloaded := uint64(downloadedPieces*int(t.Info.PieceSize)) - t.Info.HeaderSize
		if uint64(downloadedPieces*int(t.Info.PieceSize)) < t.Info.HeaderSize { // 0 if header not fully downloaded
			downloaded = 0
		}
		if downloaded > full { // cut not full last piece
			downloaded = full
		}

		var dow, upl, num uint64
		for _, p := range t.GetPeers() {
			dow += p.GetDownloadSpeed()
			upl += p.GetUploadSpeed()
			num++
		}

		table = append(table, []string{hex.EncodeToString(t.BagID), t.Info.Description.Value,
			storage.ToSz(downloaded), storage.ToSz(full), fmt.Sprint(num),
			storage.ToSpeed(dow), storage.ToSpeed(upl), fmt.Sprint(downloaded == full)})
	}

	if len(table) > 1 {
		pterm.Println("Active bags")
		pterm.DefaultTable.WithHasHeader().WithBoxed().WithData(table).Render()
	}
}
