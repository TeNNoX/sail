package environment

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types"
	"golang.org/x/xerrors"
)

type Environment struct {
	name string
	cnt  types.ContainerJSON
}

var ErrMissingContainer = xerrors.Errorf("missing container")

// FindEnvironment tries to find a container for an environment, returning
// ErrMissingContainer if not found.
func FindEnvironment(ctx context.Context, name string) (*Environment, error) {
	cli := dockerClient()
	defer cli.Close()

	cnt, err := cli.ContainerInspect(ctx, name)
	if isContainerNotFoundError(err) {
		return nil, ErrMissingContainer
	}
	if err != nil {
		return nil, xerrors.Errorf("failed to inspect container: %w", err)
	}

	env := &Environment{
		name: name,
		cnt:  cnt,
	}

	// Start it up.
	err = Start(ctx, env)
	if err != nil {
		return nil, err
	}

	return env, nil
}

func Start(ctx context.Context, env *Environment) error {
	cli := dockerClient()
	defer cli.Close()

	err := cli.ContainerStart(ctx, env.name, types.ContainerStartOptions{})
	if err != nil {
		return xerrors.Errorf("failed to start container: %w", err)
	}

	return nil
}

func Stop(ctx context.Context, env *Environment) error {
	cli := dockerClient()
	defer cli.Close()

	err := cli.ContainerStop(ctx, env.name, nil)
	if err != nil {
		return xerrors.Errorf("failed to stop container: %w", err)
	}

	return nil
}

func Remove(ctx context.Context, env *Environment) error {
	cli := dockerClient()
	defer cli.Close()

	err := cli.ContainerRemove(ctx, env.name, types.ContainerRemoveOptions{})
	if err != nil {
		return xerrors.Errorf("failed to remove container: %w", err)
	}

	return nil
}

func Purge(ctx context.Context, env *Environment) error {
	err := Stop(ctx, env)
	if err != nil {
		return err
	}
	err = Remove(ctx, env)
	if err != nil {
		return err
	}

	return nil
}

func (e *Environment) Exec(ctx context.Context, cmd string, args ...string) *exec.Cmd {
	args = append([]string{"exec", "-i", e.name, cmd}, args...)
	return exec.CommandContext(ctx, "docker", args...)
}

func (e *Environment) ExecTTY(ctx context.Context, cmd string, args ...string) *exec.Cmd {
	args = append([]string{"exec", "-it", e.name, cmd}, args...)
	return exec.CommandContext(ctx, "docker", args...)
}

var errNoSuchFile = xerrors.Errorf("no such file")

// readPath reads a path inside the environment. The returned reader is suitable
// for use with a tar reader.
//
// The root of the tar archive will be '.'
// E.g. if path is '/tmp/somedir', a file exists at '/tmp/somedir/file', the tar
// header name will be 'file'.
func (e *Environment) readPath(ctx context.Context, path string) (io.Reader, error) {
	cli := dockerClient()
	defer cli.Close()

	rdr, _, err := cli.CopyFromContainer(ctx, e.name, path)
	if isPathNotFound(err) {
		return nil, errNoSuchFile
	}
	if err != nil {
		return nil, xerrors.Errorf("failed to get reader for path '%s': %w", path, err)
	}
	defer rdr.Close()

	var (
		buf bytes.Buffer

		base = filepath.Base(path)

		tr = tar.NewReader(rdr)
		tw = tar.NewWriter(&buf)
	)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, xerrors.Errorf("failed to read from tar reader: %w", err)
		}

		hdr.Name = strings.TrimLeft(hdr.Name, base+"/")
		err = tw.WriteHeader(hdr)
		if err != nil {
			return nil, xerrors.Errorf("failed to write header: %w", err)
		}

		_, err = io.Copy(tw, tr)
		if err != nil {
			return nil, xerrors.Errorf("failed to copy: %w", err)
		}
	}
	err = tw.Close()
	if err != nil {
		return nil, xerrors.Errorf("failed to close tar writer: %w", err)
	}

	return &buf, nil
}

func isPathNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "No such container:path")
}

func isContainerNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "No such container")
}
