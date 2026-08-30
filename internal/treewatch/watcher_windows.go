//go:build windows

package treewatch

import (
	"errors"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsWatcher struct {
	handle  windows.Handle
	exited  chan struct{}
	ready   chan error
	once    sync.Once
	mu      sync.Mutex
	closing bool
}

func Start(root string, _ []string, onChange func(string), _ func(string), onFailure func(error)) (Watcher, error) {
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		path,
		windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return nil, err
	}
	watcher := &windowsWatcher{handle: handle, exited: make(chan struct{}), ready: make(chan error, 1)}
	go watcher.run(filepath.Clean(root), onChange, onFailure)
	if err := <-watcher.ready; err != nil {
		<-watcher.exited
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return watcher, nil
}

func (watcher *windowsWatcher) run(root string, onChange func(string), onFailure func(error)) {
	defer close(watcher.exited)
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		watcher.ready <- err
		return
	}
	defer windows.CloseHandle(event)
	const notifyMask = windows.FILE_NOTIFY_CHANGE_FILE_NAME |
		windows.FILE_NOTIFY_CHANGE_DIR_NAME |
		windows.FILE_NOTIFY_CHANGE_ATTRIBUTES |
		windows.FILE_NOTIFY_CHANGE_SIZE |
		windows.FILE_NOTIFY_CHANGE_LAST_WRITE |
		windows.FILE_NOTIFY_CHANGE_CREATION |
		windows.FILE_NOTIFY_CHANGE_SECURITY
	buffer := make([]byte, 64*1024)
	ready := false
	for {
		watcher.mu.Lock()
		if watcher.closing {
			watcher.mu.Unlock()
			return
		}
		overlapped := windows.Overlapped{HEvent: event}
		var length uint32
		err := windows.ReadDirectoryChanges(watcher.handle, &buffer[0], uint32(len(buffer)), true, notifyMask, &length, &overlapped, 0)
		watcher.mu.Unlock()
		if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
			if !ready {
				watcher.ready <- err
				return
			}
			if watcher.isClosing() {
				return
			}
			onFailure(err)
			return
		}
		if !ready {
			watcher.ready <- nil
			ready = true
		}
		if _, err = windows.WaitForSingleObject(event, windows.INFINITE); err != nil {
			if watcher.isClosing() {
				return
			}
			onFailure(err)
			return
		}
		err = windows.GetOverlappedResult(watcher.handle, &overlapped, &length, false)
		if err != nil {
			if watcher.isClosing() && errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				return
			}
			onFailure(err)
			return
		}
		if err := windows.ResetEvent(event); err != nil {
			onFailure(err)
			return
		}
		if length == 0 {
			onFailure(errors.New("filesystem notification buffer overflow"))
			return
		}
		for offset := uint32(0); offset < length; {
			information := (*windows.FileNotifyInformation)(unsafe.Pointer(&buffer[offset]))
			nameLength := int(information.FileNameLength / 2)
			name := windows.UTF16ToString(unsafe.Slice(&information.FileName, nameLength))
			onChange(filepath.Join(root, name))
			if information.NextEntryOffset == 0 {
				break
			}
			offset += information.NextEntryOffset
		}
	}
}

func (watcher *windowsWatcher) isClosing() bool {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	return watcher.closing
}

func (watcher *windowsWatcher) AddDirectory(string) error {
	return nil
}

func (watcher *windowsWatcher) Close() error {
	var err error
	watcher.once.Do(func() {
		watcher.mu.Lock()
		watcher.closing = true
		cancelErr := windows.CancelIoEx(watcher.handle, nil)
		watcher.mu.Unlock()
		if cancelErr != nil && !errors.Is(cancelErr, windows.ERROR_NOT_FOUND) {
			err = cancelErr
		}
		<-watcher.exited
		if closeErr := windows.CloseHandle(watcher.handle); err == nil {
			err = closeErr
		}
	})
	return err
}
