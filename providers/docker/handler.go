package docker

import (
	"fmt"
	"strings"
	"time"

	govppapi "git.fd.io/govpp.git/api"
	"git.fd.io/govpp.git/proxy"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/sirupsen/logrus"

	"go.ligato.io/vpp-probe/internal/exec"
	"go.ligato.io/vpp-probe/probe"
	"go.ligato.io/vpp-probe/providers"
	vppcli "go.ligato.io/vpp-probe/vpp/cli"
)

// ContainerHandler is used to manage an instance running in Docker.
type ContainerHandler struct {
	client    *docker.Client
	container *docker.Container

	vppProxy *proxy.Client
}

// NewHandler returns a new handler for an instance running in a pod.
func NewHandler(client *docker.Client, container *docker.Container) *ContainerHandler {
	return &ContainerHandler{
		client:    client,
		container: container,
	}
}

func (h *ContainerHandler) ID() string {
	containerID := h.container.ID
	if len(containerID) > 7 {
		containerID = containerID[:7]
	}
	return fmt.Sprintf("%v-%v", getContainerName(h.container), containerID)
}

func (h *ContainerHandler) Metadata() map[string]string {
	name := getContainerName(h.container)
	id := getContainerID(h.container)
	return map[string]string{
		"env":       providers.Docker,
		"name":      name,
		"container": name,
		"id":        id,
		"image":     h.container.Config.Image,
		"created":   h.container.Created.Format(time.UnixDate),
	}
}

func getContainerName(container *docker.Container) string {
	return strings.TrimPrefix(container.Name, "/")
}

func getContainerID(container *docker.Container) string {
	if len(container.ID) > 12 {
		return container.ID[:12]
	}
	return container.ID
}

func (h *ContainerHandler) Close() error {
	logrus.Debugf("closing handler %v", h.ID())
	h.vppProxy = nil
	return nil
}

func (h *ContainerHandler) ExecCmd(command string, args ...string) (string, error) {
	cmd := h.Command(command, args...)
	out, err := cmd.Output()
	return string(out), err
}

func (h *ContainerHandler) GetCLI() (probe.CliExecutor, error) {
	var args []string
	if err := h.Command("ls", "/run/vpp/cli.sock").Run(); err != nil {
		args = append(args, "-s", "localhost:5002")
		logrus.Tracef("checking cli socket error: %v, using flag '%s' for vppctl", err, args)
	}
	wrapper := exec.Wrap(h, "/usr/bin/vppctl", args...)
	cli := vppcli.ExecutorFunc(func(cmd string) (string, error) {
		out, err := wrapper.Command(cmd).Output()
		if err != nil {
			return "", err
		}
		return string(out), nil
	})
	return cli, nil
}

func (h *ContainerHandler) Command(cmd string, args ...string) exec.Cmd {
	return &Cmd{
		Cmd:       cmd,
		Args:      args,
		container: h,
	}
}

func (h *ContainerHandler) GetAPI() (govppapi.Channel, error) {
	if err := h.connectProxy(); err != nil {
		return nil, err
	}

	return proxyBinapi(h.vppProxy)
}

func (h *ContainerHandler) GetStats() (govppapi.StatsProvider, error) {
	if err := h.connectProxy(); err != nil {
		return nil, err
	}

	return proxyStats(h.vppProxy)
}

func (h *ContainerHandler) connectProxy() error {
	if h.vppProxy != nil {
		return nil
	}

	logrus.Tracef("network settings: %+v", h.container.NetworkSettings)

	var ipaddr string
	for _, nw := range h.container.NetworkSettings.Networks {
		if nw.IPAddress != "" {
			ipaddr = nw.IPAddress
			break
		}
	}
	addr := fmt.Sprintf("%s:%d", ipaddr, 9191)

	logrus.Debugf("connecting to proxy %v", addr)

	c, err := proxy.Connect(addr)
	if err != nil {
		return fmt.Errorf("connecting to proxy failed: %v", err)
	}
	h.vppProxy = c

	return nil
}

func proxyBinapi(client *proxy.Client) (govppapi.Channel, error) {
	binapiChannel, err := client.NewBinapiClient()
	if err != nil {
		logrus.Errorf("creating new proxy binapi client failed: %v", err)
		return nil, err
	}
	return binapiChannel, nil
}

func proxyStats(client *proxy.Client) (govppapi.StatsProvider, error) {
	statsProvider, err := client.NewStatsClient()
	if err != nil {
		return nil, err
	}
	return statsProvider, nil
}
