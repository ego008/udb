package udb

import (
	"time"

	bolt "go.etcd.io/bbolt"
)

// Options controls bbolt and UDB background maintenance.
type Options struct {
	LockTimeout     time.Duration
	FreelistType    bolt.FreelistType
	NoFreelistSync  bool
	NoSync          bool
	NoGrowSync      bool
	InitialMmapSize int
	PreLoadFreelist bool
	ReadOnly        bool
	Mlock           bool
	Logger          bolt.Logger
	Maintenance     MaintenanceConfig
}

func DefaultOptions() Options {
	return Options{
		LockTimeout:  3 * time.Second,
		FreelistType: bolt.FreelistMapType,
		Logger:       nil,
		Maintenance:  DefaultMaintenanceConfig(),
	}
}

func PerformanceOptions() Options { o := DefaultOptions(); o.NoGrowSync = true; return o }
func ReadOnlyOptions() Options {
	o := DefaultOptions()
	o.ReadOnly = true
	o.Maintenance.Enabled = false
	return o
}
func QuietOptions() Options { o := DefaultOptions(); o.Logger = nil; return o }

func normalizeOptions(in *Options) Options {
	o := DefaultOptions()
	if in != nil {
		o = *in
	}
	if o.LockTimeout <= 0 {
		o.LockTimeout = 3 * time.Second
	}
	if o.FreelistType != bolt.FreelistArrayType && o.FreelistType != bolt.FreelistMapType {
		o.FreelistType = bolt.FreelistMapType
	}
	if isZeroMaintenanceConfig(o.Maintenance) {
		o.Maintenance = DefaultMaintenanceConfig()
	}
	if o.Maintenance.Interval <= 0 {
		o.Maintenance.Interval = 30 * time.Minute
	}
	if o.Maintenance.MinDBSize < 0 {
		o.Maintenance.MinDBSize = 0
	}
	if o.Maintenance.FreeRatio < 0 {
		o.Maintenance.FreeRatio = 0
	}
	if o.Maintenance.FreeRatio > 1 {
		o.Maintenance.FreeRatio = 1
	}
	if o.Maintenance.PendingRatio < 0 {
		o.Maintenance.PendingRatio = 0
	}
	if o.Maintenance.PendingRatio > 1 {
		o.Maintenance.PendingRatio = 1
	}
	if o.Maintenance.CompactCooldown < 0 {
		o.Maintenance.CompactCooldown = 0
	}
	if o.Maintenance.MaxFailures < 0 {
		o.Maintenance.MaxFailures = 0
	}
	return o
}

// isZeroMaintenanceConfig reports whether the configuration is entirely unset.
// MaintenanceConfig contains CompactFaultInjector, a function value, so the
// struct itself cannot be compared with MaintenanceConfig{} in Go.
func isZeroMaintenanceConfig(c MaintenanceConfig) bool {
	return !c.Enabled &&
		c.Interval == 0 &&
		c.MinDBSize == 0 &&
		c.FreeRatio == 0 &&
		c.PendingRatio == 0 &&
		c.TxMaxSize == 0 &&
		!c.CheckBeforeCompact &&
		!c.CheckAfterCompact &&
		!c.KeepBackup &&
		c.BackupSuffix == "" &&
		c.CompactCooldown == 0 &&
		c.MaxFailures == 0 &&
		c.FaultInjector == nil
}

func (o Options) boltOptions() *bolt.Options {
	return &bolt.Options{
		Timeout:         o.LockTimeout,
		NoGrowSync:      o.NoGrowSync,
		NoFreelistSync:  o.NoFreelistSync,
		FreelistType:    o.FreelistType,
		ReadOnly:        o.ReadOnly,
		InitialMmapSize: o.InitialMmapSize,
		PreLoadFreelist: true,
		PageSize:        0,
		NoSync:          o.NoSync,
		Mlock:           o.Mlock,
		Logger:          o.Logger,
	}
}
