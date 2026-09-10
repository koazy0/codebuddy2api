package zap

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// cutter 实现日志切割
type cutter struct {
	level        string
	layout       string
	formats      []string
	director     string
	retentionDay int
	file         *os.File
	mutex        *sync.RWMutex
}

type cutterOption func(*cutter)

func cutterWithLayout(layout string) cutterOption {
	return func(c *cutter) { c.layout = layout }
}

func cutterWithFormats(format ...string) cutterOption {
	return func(c *cutter) {
		if len(format) > 0 {
			c.formats = format
		}
	}
}

func newCutter(director string, level string, retentionDay int, options ...cutterOption) *cutter {
	c := &cutter{
		level:        level,
		director:     director,
		retentionDay: retentionDay,
		mutex:        new(sync.RWMutex),
	}
	for i := range options {
		options[i](c)
	}
	return c
}

func (c *cutter) Write(bytes []byte) (n int, err error) {
	c.mutex.Lock()
	defer func() {
		if c.file != nil {
			_ = c.file.Close()
			c.file = nil
		}
		c.mutex.Unlock()
	}()

	values := make([]string, 0, 3+len(c.formats))
	values = append(values, c.director)
	if c.layout != "" {
		values = append(values, time.Now().Format(c.layout))
	}
	for i := range c.formats {
		values = append(values, c.formats[i])
	}
	values = append(values, c.level+".log")
	filename := filepath.Join(values...)

	if err = os.MkdirAll(filepath.Dir(filename), os.ModePerm); err != nil {
		return 0, err
	}
	if err = removeNDaysFolders(c.director, c.retentionDay); err != nil {
		return 0, err
	}
	c.file, err = os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return 0, err
	}
	return c.file.Write(bytes)
}

func (c *cutter) Sync() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.file != nil {
		return c.file.Sync()
	}
	return nil
}

func removeNDaysFolders(dir string, days int) error {
	if days <= 0 {
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.ModTime().Before(cutoff) && path != dir {
			return os.RemoveAll(path)
		}
		return nil
	})
}
