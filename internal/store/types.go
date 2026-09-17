package store

import "time"

type User struct {
	ID           int64
	Name         string
	PasswordHash string
	IsAdmin      bool
	CreatedAt    int64
	UpdatedAt    int64
}

type Library struct {
	ID        int64
	Name      string
	Type      string
	Path      string
	CreatedAt int64
}

type Work struct {
	ID          int64
	LibraryID   int64
	Title       string
	Subtitle    *string
	Author      *string
	Description *string
	CoverPath   *string
	CreatedAt   int64
	UpdatedAt   int64
	Created     bool
}

type Edition struct {
	ID           int64
	WorkID       int64
	Format       string
	Title        string
	Language     *string
	Abridged     bool
	DurationSecs *float64
	Position     int64
	CreatedAt    int64
	SeasonNum    *int
	EpisodeNum   *int
}

type FileRec struct {
	ID           int64
	EditionID    int64
	Path         string
	Seq          int
	SizeBytes    int64
	MtimeSecs    int64
	MtimeNS      int64
	Hash         *string
	Codec        *string
	VideoCodec   *string
	Width        *int
	Height       *int
	Container    *string
	Bitrate      *int64
	Channels     *int
	SampleRate   *int
	DurationSecs float64
	Chapters     string
	Missing      bool
	Inserted     bool
}

type Progress struct {
	UserID              int64
	EditionID           int64
	FileID              *int64
	FileOffsetSecs      float64
	EditionPositionSecs float64
	DurationSecs        *float64
	IsFinished          bool
	Device              *string
	UpdatedAt           int64
}

type Session struct {
	ID           string
	UserID       int64
	EditionID    int64
	FileID       *int64
	StartedAt    int64
	UpdatedAt    int64
	PositionSecs float64
	TimeListened float64
	DeviceInfo   string
	ClosedAt     *int64
}

func nowMilli() int64 { return time.Now().UnixMilli() }
