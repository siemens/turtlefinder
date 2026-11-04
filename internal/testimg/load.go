// (c) Siemens AG 2026
//
// SPDX-License-Identifier: MIT

package testimg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/moby/moby/client"
	"github.com/thediveo/morbyd/v2"
	"github.com/thediveo/morbyd/v2/exec"
)

// morbyd's client interface lacks ImageSave, but its underlying client has it.
type imageSaver interface {
	ImageSave(ctx context.Context, images []string, opts ...client.ImageSaveOption) (client.ImageSaveResult, error)
}

// Load saves the image referenced by imgref from the Docker engine of cntr's
// session and pipes it as an image archive into loadcmd run inside cntr, such
// as “ctr image import -” or “podman load”. The image gets pulled into the
// Docker engine only when not already present. The output of loadcmd goes to
// out.
func Load(ctx context.Context, cntr *morbyd.Container, imgref string, out io.Writer, loadcmd ...string) error {
	sess := cntr.Session
	has, err := sess.HasImage(ctx, imgref)
	if err != nil {
		return err
	}
	if !has {
		if err := sess.PullImage(ctx, imgref); err != nil {
			return err
		}
	}
	saver, ok := sess.Client().(imageSaver)
	if !ok {
		return fmt.Errorf("Docker client %T cannot save images", sess.Client()) //nolint:staticcheck // ST1005: "Docker" is a proper noun
	}
	archive, err := saver.ImageSave(ctx, []string{imgref})
	if err != nil {
		return err
	}
	data, err := io.ReadAll(archive)
	_ = archive.Close()
	if err != nil {
		return err
	}
	// morbyd never closes the exec's stdin, so head has to deliver EOF to loadcmd.
	loader, err := cntr.Exec(ctx,
		exec.Command("sh", append([]string{"-c", `head -c "$0" | "$@"`, strconv.Itoa(len(data))}, loadcmd...)...),
		exec.WithInput(bytes.NewReader(data)),
		exec.WithCombinedOutput(out))
	if err != nil {
		return err
	}
	exitcode, err := loader.Wait(ctx)
	if err != nil {
		return err
	}
	if exitcode != 0 {
		return fmt.Errorf("%q failed with exit code %d", strings.Join(loadcmd, " "), exitcode)
	}
	return nil
}
