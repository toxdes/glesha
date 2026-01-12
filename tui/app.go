package tui

import (
	"context"
	"glesha/database"
	"glesha/database/model"
	"glesha/database/repository"
	L "glesha/logger"
	"glesha/tui/components"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type focusArea int

const (
	focusSidebar focusArea = iota
	focusContent
)

type tabId int

const (
	tabStatus tabId = iota
	tabFiles
)

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

type modelTui struct {
	ctx            context.Context
	db             *database.DB
	taskRepo       repository.TaskRepository
	uploadRepo     repository.UploadRepository
	catalogRepo    repository.FileCatalogRepository
	tasks          []components.TaskInfo
	files          []model.FileCatalogRow
	sidebarCursor  int
	contentCursor  int
	contentOffset  int
	selectedTaskId int64
	currentDir     string
	focus          focusArea
	activeTab      tabId
	width          int
	height         int
}

func NewApp(ctx context.Context, db *database.DB) *modelTui {
	return &modelTui{
		ctx:         ctx,
		db:          db,
		taskRepo:    repository.NewTaskRepository(db),
		uploadRepo:  repository.NewUploadRepository(db),
		catalogRepo: repository.NewFileCatalogRepository(db),
		currentDir:  ".",
		focus:       focusSidebar,
		activeTab:   tabStatus,
	}
}

func (m modelTui) Init() tea.Cmd {
	return tea.Batch(m.fetchTasks, tick())
}

func (m modelTui) fetchTasks() tea.Msg {
	tasks, err := m.taskRepo.ListTasks(m.ctx)
	if err != nil {
		L.Error("tui: failed to fetch tasks: %v", err)
		return []components.TaskInfo{}
	}

	var taskInfos []components.TaskInfo
	for _, t := range tasks {
		up, err := m.uploadRepo.GetUploadByTaskId(m.ctx, t.Id)
		if err != nil {
			// upload may not exist yet
			L.Debug("tui: no upload found for task %d: %v", t.Id, err)
			up = nil
		}
		taskInfos = append(taskInfos, components.TaskInfo{Task: t, Upload: up})
	}
	return taskInfos
}

func (m modelTui) fetchFiles() tea.Msg {
	if m.selectedTaskId == 0 {
		return nil
	}
	files, err := m.catalogRepo.GetByParentPath(m.ctx, m.selectedTaskId, m.currentDir)
	if err != nil {
		L.Error("tui: failed to fetch files for task %d: %v", m.selectedTaskId, err)
		return []model.FileCatalogRow{}
	}
	return files
}

func (m *modelTui) goBack() {
	if m.currentDir != "." {
		m.currentDir = filepath.Dir(m.currentDir)
		m.contentCursor = 0
	}
}

func (m *modelTui) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		return m, tea.Batch(m.fetchTasks, tick())

	case []components.TaskInfo:
		m.tasks = msg
		if m.selectedTaskId == 0 && len(m.tasks) > 0 {
			m.selectedTaskId = m.tasks[0].Task.Id
			return m, m.fetchFiles
		}

	case []model.FileCatalogRow:
		m.files = msg

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit

		case "1":
			m.focus = focusSidebar

		case "2":
			m.focus = focusContent

		case "tab":
			if m.focus == focusSidebar {
				m.focus = focusContent
			} else {
				m.focus = focusSidebar
			}

		case "3", "s", "S":
			if m.focus == focusContent {
				m.activeTab = tabStatus
			}

		case "4", "f", "F":
			if m.focus == focusContent {
				m.activeTab = tabFiles
				return m, m.fetchFiles
			}

		case "up", "k":
			if m.focus == focusSidebar {
				if m.sidebarCursor > 0 {
					m.sidebarCursor--
					m.selectedTaskId = m.tasks[m.sidebarCursor].Task.Id
					m.contentCursor = 0
					m.contentOffset = 0
					return m, m.fetchFiles
				}
			} else if m.activeTab == tabFiles {
				if m.contentCursor > 0 {
					m.contentCursor--
					if m.contentCursor < m.contentOffset {
						m.contentOffset = m.contentCursor
					}
				}
			}

		case "down", "j":
			if m.focus == focusSidebar {
				if m.sidebarCursor < len(m.tasks)-1 {
					m.sidebarCursor++
					m.selectedTaskId = m.tasks[m.sidebarCursor].Task.Id
					m.contentCursor = 0
					m.contentOffset = 0
					return m, m.fetchFiles
				}
			} else if m.activeTab == tabFiles {
				if m.contentCursor < len(m.files) {
					m.contentCursor++
					// handwaving space for file list
					maxVisible := m.height - 15
					if m.contentCursor >= m.contentOffset+maxVisible {
						m.contentOffset = m.contentCursor - maxVisible + 1
					}
				}
			}

		case "enter", "l", "right":
			if m.focus == focusContent && m.activeTab == tabFiles {
				if m.contentCursor == 0 {
					m.goBack()
					return m, m.fetchFiles
				} else if len(m.files) >= m.contentCursor {
					f := m.files[m.contentCursor-1]
					if f.FileType == "dir" {
						m.currentDir = f.FullPath
						m.contentCursor = 0
						return m, m.fetchFiles
					}
				}
			}

		case "esc", "h", "left":
			if m.focus == focusContent && m.activeTab == tabFiles {
				m.goBack()
				return m, m.fetchFiles
			}
		}
	}

	return m, nil
}
