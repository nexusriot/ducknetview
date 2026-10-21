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

func TestCgroupInfoOfNonexistentPID(t *testing.T) {
	if c, u := CgroupInfo(-1); c != "" || u != "" {
		t.Errorf("CgroupInfo(-1) = %q, %q, want empty", c, u)
	}
}

func TestParseCgroupUnit(t *testing.T) {
	const id = "3f2a1b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708"

	cases := []struct {
		name     string
		contents string
		want     string
	}{
		{
			name:     "system service on cgroup v2",
			contents: "0::/system.slice/nginx.service\n",
			want:     "nginx.service",
		},
		{
			name:     "service sub-cgroup under delegation",
			contents: "0::/system.slice/docker.service/supervisor\n",
			want:     "docker.service",
		},
		{
			name:     "login session scope",
			contents: "0::/user.slice/user-1000.slice/session-2.scope\n",
			want:     "session-2.scope",
		},
		{
			name:     "user service under the user manager",
			contents: "0::/user.slice/user-1000.slice/user@1000.service/app.slice/pipewire.service\n",
			want:     "pipewire.service",
		},
		{
			name:     "user app scope",
			contents: "0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-org.gnome.Terminal.slice/vte-spawn-5d1a.scope\n",
			want:     "vte-spawn-5d1a.scope",
		},
		{
			name:     "user manager itself",
			contents: "0::/user.slice/user-1000.slice/user@1000.service/init.scope\n",
			want:     "init.scope",
		},
		{
			name: "cgroup v1 prefers the systemd hierarchy",
			contents: "12:memory:/\n" +
				"11:pids:/system.slice/sshd.service\n" +
				"1:name=systemd:/system.slice/sshd.service\n",
			want: "sshd.service",
		},
		{
			name: "cgroup v1 controller fallback when name=systemd is at the root",
			contents: "4:pids:/system.slice/cron.service\n" +
				"1:name=systemd:/\n",
			want: "cron.service",
		},
		{
			name: "hybrid hierarchy prefers unified",
			contents: "1:name=systemd:/system.slice/postgresql.service\n" +
				"0::/system.slice/postgresql@16-main.service\n",
			want: "postgresql@16-main.service",
		},
		{
			name:     "docker container scope is not a unit",
			contents: "0::/system.slice/docker-" + id + ".scope\n",
			want:     "",
		},
		{
			name:     "podman rootless container scope is not a unit",
			contents: "0::/user.slice/user-1000.slice/user@1000.service/user.slice/libpod-" + id + ".scope/container\n",
			want:     "",
		},
		{
			name:     "kubernetes pod",
			contents: "0::/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod9a1b.slice/cri-containerd-" + id + ".scope\n",
			want:     "",
		},
		{
			name:     "cgroupfs docker v1",
			contents: "12:pids:/docker/" + id + "\n1:name=systemd:/docker/" + id + "\n",
			want:     "",
		},
		{
			name:     "slice only",
			contents: "0::/system.slice\n",
			want:     "",
		},
		{
			name:     "inside a cgroup namespace",
			contents: "0::/\n",
			want:     "",
		},
		{
			name:     "empty",
			contents: "",
			want:     "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseCgroupUnit(tc.contents); got != tc.want {
				t.Errorf("parseCgroupUnit = %q, want %q", got, tc.want)
			}
		})
	}
}

// The container and the unit come from the same path; a containerised process
// must not have its container's scope reported as its unit as well.
func TestParseCgroupContainerAndUnitAgree(t *testing.T) {
	const id = "3f2a1b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708"
	contents := "0::/system.slice/docker-" + id + ".scope\n"
	if c, u := parseCgroupContainer(contents), parseCgroupUnit(contents); c != id[:12] || u != "" {
		t.Errorf("container %q unit %q, want %q and none", c, u, id[:12])
	}
}
