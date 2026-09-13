package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/maestroyoyo/ghostchat/internal/network"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type sessionState int

const (
	menuState sessionState = iota
	joinRoomState
	chatState
	nickState
)

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212")).
			Bold(true).
			Padding(0, 1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63"))

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(1, 2).
			Width(85)

	systemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	meStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	peerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	codeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
)

type model struct {
	state     sessionState
	cursor    int
	opciones  []string
	input     textinput.Model
	statusMsg string
	chatLogs  []string
	nickname  string // [!] CORREGIDO: El apodo ahora se gestiona en la interfaz
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "Escribe aquí..."
	ti.Focus()
	ti.CharLimit = 150
	ti.Width = 65

	return model{
		state:    menuState,
		opciones: []string{"Crear Sala Blindada (Generar Código)", "Unirse a Sala con Código", "Cambiar mi Apodo", "Salir"},
		input:    ti,
		chatLogs: []string{},
		nickname: "Anon", // Apodo por defecto al abrir la app
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, waitForMessage())
}

func waitForMessage() tea.Cmd {
	return func() tea.Msg {
		msg := <-network.IncomingMessages
		return msg
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case string:
		if strings.HasPrefix(msg, "[-] ") || strings.HasPrefix(msg, "[!]") {
			m.statusMsg = msg
		} else {
			m.chatLogs = append(m.chatLogs, msg)
		}
		return m, waitForMessage()

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			network.DestroySession()
			return m, tea.Quit
		}

		switch m.state {
		case menuState:
			switch msg.String() {
			case "q":
				return m, tea.Quit
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down", "j":
				if m.cursor < len(m.opciones)-1 {
					m.cursor++
				}
			case "enter":
				switch m.cursor {
				case 0:
					invite := network.GenerateSecureInvite()
					m.chatLogs = []string{
						"[Sistema] Código de invitación seguro generado:",
						"👉 " + invite,
						"[*] Esperando que tu contacto se conecte a la red...",
					}
					m.state = chatState
					m.input.SetValue("")
					m.input.Placeholder = "Mensaje o /file [ruta]..."
					// [!] CORRECCIÓN: el creador también debe unirse a la sala con su
					// propio código (deriva la misma clave y anuncia el tema en la DHT).
					// En v1 este paso faltaba y la red nunca se iniciaba.
					go network.ConnectToRoomWithInvite(invite)
					return m, tea.Batch(textinput.Blink, waitForMessage())
				case 1:
					m.state = joinRoomState
					m.input.SetValue("")
					m.input.Placeholder = "Pega aquí el código de invitación..."
					return m, textinput.Blink
				case 2:
					m.state = nickState
					m.input.SetValue(m.nickname) // [!] Usando la variable local
					m.input.Placeholder = "Tu apodo..."
					return m, textinput.Blink
				case 3:
					return m, tea.Quit
				}
			}

		case joinRoomState:
			switch msg.String() {
			case "esc":
				m.state = menuState
			case "enter":
				code := strings.TrimSpace(m.input.Value())
				if code != "" {
					// Conexión asíncrona para no bloquear la interfaz durante
					// el arranque de la DHT; el fallo se reporta en el chat.
					m.chatLogs = []string{"[Sistema] Conectando a la sala..."}
					m.state = chatState
					m.input.SetValue("")
					m.input.Placeholder = "Mensaje o /file [ruta]..."
					go func(c string) {
						if err := network.ConnectToRoomWithInvite(c); err != nil {
							network.IncomingMessages <- "[!] Error al conectar: " + err.Error()
						}
					}(code)
					return m, tea.Batch(textinput.Blink, waitForMessage())
				}
			default:
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}

		case nickState:
			switch msg.String() {
			case "esc":
				m.state = menuState
			case "enter":
				nick := strings.TrimSpace(m.input.Value())
				if nick != "" {
					m.nickname = nick // [!] Guardamos el apodo localmente
					m.statusMsg = "Apodo actualizado a: " + nick
				}
				m.state = menuState
			default:
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}

		case chatState:
			switch msg.String() {
			case "esc":
				network.DestroySession()
				m.chatLogs = []string{"[Sistema] Sesión destruida de forma segura (cero rastro en RAM/Disco)."}
				m.state = menuState
			case "enter":
				texto := m.input.Value()
				if texto != "" {
					if strings.HasPrefix(texto, "/file ") {
						filePath := strings.TrimSpace(strings.TrimPrefix(texto, "/file "))
						filePath = strings.Trim(filePath, `"'`)
						m.chatLogs = append(m.chatLogs, fmt.Sprintf("[%s]: [Enviando archivo...]", m.nickname))
						go network.SendFile(filePath)
					} else {
						m.chatLogs = append(m.chatLogs, fmt.Sprintf("[%s]: %s", m.nickname, texto)) // [!] Usando apodo local
						go network.SendMessage(texto)
					}
					m.input.SetValue("")
				}
			default:
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
		}
	}
	return m, nil
}

func (m model) View() string {
	title := titleStyle.Render("GHOSTCHAT // FORTIFIED SECURE E2EE P2P")
	var content string

	switch m.state {
	case menuState:
		menuStr := "\n"
		for i, opcion := range m.opciones {
			cursor := "  "
			if m.cursor == i {
				cursor = "▶ "
			}
			if m.cursor == i {
				menuStr += fmt.Sprintf("%s%s\n", cursor, lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true).Render(opcion))
			} else {
				menuStr += fmt.Sprintf("%s%s\n", cursor, opcion)
			}
		}
		if m.statusMsg != "" {
			menuStr += fmt.Sprintf("\n%s\n", systemStyle.Render(m.statusMsg))
		}
		menuStr += "\n(Usa flechas para moverte, Enter para seleccionar, 'q' para salir)"
		content = title + "\n" + boxStyle.Render(menuStr)

	case joinRoomState:
		form := fmt.Sprintf("Introduce el Código de Invitación Seguro:\n\n%s\n\n(Pulsa Enter para conectar, Esc para volver)", m.input.View())
		content = title + "\n" + boxStyle.Render(form)

	case nickState:
		form := fmt.Sprintf("Configura tu apodo anónimo:\n\n%s\n\n(Pulsa Enter para guardar, Esc para volver)", m.input.View())
		content = title + "\n" + boxStyle.Render(form)

	case chatState:
		chatStr := "--- SALA BLINDADA ACTIVA (Pulsa Esc para destruir sesión) ---\n\n"
		start := 0
		if len(m.chatLogs) > 10 {
			start = len(m.chatLogs) - 10
		}
		for _, log := range m.chatLogs[start:] {
			if strings.HasPrefix(log, "[Sistema]") || strings.HasPrefix(log, "[*]") {
				chatStr += systemStyle.Render(log) + "\n"
			} else if strings.HasPrefix(log, "👉") {
				chatStr += codeStyle.Render(log) + "\n"
			} else if strings.Contains(log, "[-] ") || strings.Contains(log, "[!]") {
				chatStr += errorStyle.Render(log) + "\n"
			} else if strings.Contains(log, "["+m.nickname+"]") { // [!] Match exacto con tu propio apodo visual
				chatStr += meStyle.Render(log) + "\n"
			} else {
				chatStr += peerStyle.Render(log) + "\n"
			}
		}
		if m.statusMsg != "" {
			chatStr += errorStyle.Render(m.statusMsg) + "\n"
		}
		chatStr += "\n" + m.input.View()
		content = title + "\n" + boxStyle.Render(chatStr)
	}

	return "\n" + content + "\n"
}

func Start() {
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error al arrancar: %v", err)
		os.Exit(1)
	}
}
