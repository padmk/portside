package cli

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/share"
)

// cmdShare shows or changes whether the owner's other devices may connect,
// issues pairing codes and manages paired devices.
func cmdShare(e *env, args []string) error {
	c, err := e.client()
	if err != nil {
		return err
	}
	var ov server.RemoteOverview
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch {
	case sub == "" && len(args) == 0:
		err = c.Do(e.ctx, http.MethodGet, "/api/remote", nil, &ov)
	case (sub == "on" || sub == "off") && len(args) == 1:
		err = c.Do(e.ctx, http.MethodPut, "/api/remote/share", map[string]bool{"enabled": sub == "on"}, &ov)
	case sub == "code" && len(args) == 1:
		return printCode(e, c)
	case sub == "devices" && len(args) == 1:
		if err := c.Do(e.ctx, http.MethodGet, "/api/remote", nil, &ov); err != nil {
			return err
		}
		printDevices(e, ov.Share.Devices)
		return nil
	case sub == "revoke" && len(args) == 2:
		if err := c.Do(e.ctx, http.MethodDelete, "/api/remote/share/devices/"+url.PathEscape(args[1]), nil, &ov); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "%s removed %s\n", e.paint(green, "●"), args[1])
		return nil
	default:
		return usageError{}
	}
	if err != nil {
		return err
	}
	st := ov.Share
	switch {
	case !st.Enabled:
		fmt.Fprintf(e.out, "%s sharing is off\n", e.paint(dim, "•"))
	case st.Active:
		fmt.Fprintf(e.out, "%s shared with your devices on Tailscale at %s\n", e.paint(green, "●"), st.URL)
	default:
		fmt.Fprintf(e.out, "%s sharing is on but not active: %s\n", e.paint(red, "●"), st.Error)
	}
	if st.Enabled && sub == "on" {
		return printCode(e, c)
	}
	if st.Enabled && sub == "" {
		printDevices(e, st.Devices)
	}
	return nil
}

func printCode(e *env, c *client.Client) error {
	var code share.Code
	if err := c.Do(e.ctx, http.MethodPost, "/api/remote/share/code", nil, &code); err != nil {
		return err
	}
	fmt.Fprintf(e.out, "\n  pairing code  %s\n\n", e.paint(green, code.Code))
	fmt.Fprintf(e.out, "Enter it on the other computer: Computers, then Pair. It works once and expires at %s.\n",
		code.ExpiresAt.Local().Format("15:04"))
	return nil
}

func printDevices(e *env, devs []share.Device) {
	if len(devs) == 0 {
		fmt.Fprintln(e.out, "No paired devices.")
		return
	}
	for _, d := range devs {
		fmt.Fprintf(e.out, "  %s  %s  %s  paired %s\n", e.paint(dim, d.ID), d.Name, d.DNSName, d.PairedAt.Local().Format("2006-01-02"))
	}
}
