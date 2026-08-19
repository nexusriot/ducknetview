package probe

import "testing"

func TestParseCgroupContainer(t *testing.T) {
	const id = "3f2a1b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708"

	cases := []struct {
		name     string
		contents string
		want     string
	}{
		{
			name:     "docker cgroup v1",
			contents: "12:pids:/docker/" + id + "\n11:cpu:/docker/" + id + "\n",
			want:     id[:12],
		},
		{
			name:     "docker cgroup v2 systemd scope",
			contents: "0::/system.slice/docker-" + id + ".scope\n",
			want:     id[:12],
		},
		{
			name:     "podman",
			contents: "0::/user.slice/user-1000.slice/user@1000.service/libpod-" + id + ".scope\n",
			want:     id[:12],
		},
		{
			name:     "cri-containerd under kubepods",
			contents: "0::/kubepods/burstable/pod9a1b/cri-containerd-" + id + ".scope\n",
			want:     id[:12],
		},
		{
			name:     "lxc by name",
			contents: "0::/lxc/web01\n",
			want:     "web01",
		},
		{
			name:     "plain host process",
			contents: "0::/user.slice/user-1000.slice/session-2.scope\n",
			want:     "",
		},
		{
			name:     "systemd service is not a container",
			contents: "0::/system.slice/nginx.service\n",
			want:     "",
		},
		{
			name:     "empty",
			contents: "",
			want:     "",
		},
		{
			name:     "malformed lines are skipped",
			contents: "not-a-cgroup-line\n0::/docker/" + id + "\n",
			want:     id[:12],
		},
		{
			name:     "hex-looking but wrong length",
			contents: "0::/docker/" + id[:40] + "\n",
			want:     "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseCgroupContainer(c.contents); got != c.want {
				t.Errorf("parseCgroupContainer() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestContainerIDOfNonexistentPID(t *testing.T) {
	if got := ContainerID(-1); got != "" {
		t.Errorf("ContainerID(-1) = %q, want empty", got)
	}
}
