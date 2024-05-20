package handler

import (
	"net/http"
	"oatorrent/internal/pkg/bt"
	"oatorrent/internal/pkg/conf"
	"oatorrent/internal/repo"

	"github.com/anacrolix/torrent/bencode"
	"github.com/gin-gonic/gin"
)

type Repoer interface {
	PutPeer(room, info_hash string, peer *bt.Peer, seed bool)
	GetPeers(room, info_hash string, peer *bt.Peer, seed bool, num_want uint) []*bt.Peer
	DeletePeer(room, info_hash string, peer *bt.Peer, seed bool)
	GraduateLeecher(room, info_hash string, peer *bt.Peer)
	CountPeers(room, info_hash string, v6 bool) (num_seeders, num_snachers, num_leechers uint)
	Cleanup()
}

// Interface check
var _ Repoer = (*repo.MemRepo)(nil)

type Handler struct {
	cfg  *conf.Config
	repo Repoer
}

func NewHandler(cfg *conf.Config) *Handler {
	return &Handler{
		cfg:  cfg,
		repo: repo.NewMemRepo(cfg.NumShard),
	}
}

func (h *Handler) Announce(c *gin.Context) {
	var req = &bt.AnnounceReq{
		Compact:   true,
		NumWant:   30,
		IP:        c.ClientIP(),
		TrackerID: h.cfg.TrackerID,
	}
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var req_peer = bt.NewPeer(req.PeerID, req.IP, req.Port)
	var room = c.Param("room")
	if room == "" {
		c.JSON(400, gin.H{"error": "room is required"})
	}
	switch req.Event {
	// case "started": behaves the same as default
	case "stopped":
		h.repo.DeletePeer(room, req.InfoHash, req_peer, *req.Left == 0)
	case "completed":
		h.repo.GraduateLeecher(room, req.InfoHash, req_peer)
	default:
		h.repo.PutPeer(room, req.InfoHash, req_peer, *req.Left == 0)
	}

	interval, min_interval := h.cfg.Interval, h.cfg.MinInterval

	num_seeders, _, num_leechers := h.repo.CountPeers(room, req.InfoHash, req_peer.Addr().Is6())
	if num_seeders == 0 {
		interval /= 2
		min_interval /= 2
	}

	var peers = h.repo.GetPeers(room, req.InfoHash, req_peer, *req.Left == 0, req.NumWant)
	var packed_peer any
	if req.Compact {
		packed_peer = make([]byte, 0, len(peers))
		for _, peer := range peers {
			packed_peer = append(packed_peer.([]byte), peer.PackToBin()...)
		}
	} else {
		packed_peer = make([]map[string]any, 0, len(peers))
		for _, peer := range peers {
			packed_peer = append(packed_peer.([]map[string]any), peer.PackToDict())
		}
	}

	var resp = &bt.AnnounceResp{
		Interval:    interval,
		MinInterval: min_interval,
		TrackerID:   req.TrackerID,
		Complete:    num_seeders,
		Incomplete:  num_leechers,
		Peers:       packed_peer,
	}
	resp_data, err := bencode.Marshal(resp)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.String(http.StatusOK, string(resp_data))
}

func (h *Handler) Scrape(c *gin.Context) {
	var req *bt.ScrapeReq
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var resp = &bt.ScrapeResp{Files: make(map[string]bt.Stats)}
	for _, info_hash := range req.InfoHashes {
		s, sn, l := h.repo.CountPeers("", info_hash, false)
		s_v6, sn_v6, l_v6 := h.repo.CountPeers("", info_hash, true)
		var stats = &bt.Stats{
			Complete:   s + s_v6,
			Downloaded: sn + sn_v6,
			Incomplete: l + l_v6,
		}
		resp.Files[info_hash] = *stats
	}
	resp_data, err := bencode.Marshal(resp)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.String(http.StatusOK, string(resp_data))
}
