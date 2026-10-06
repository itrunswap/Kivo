package platform

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func proxyTestSnapshot() ProxySnapshot {
	return ProxySnapshot{Backend: "test", Scope: "user-a", Entries: []ProxyEntry{{Name: "wifi", Values: map[string]string{"mode": "off", "server": "old:80", "bypass": "local", "pac": "https://private.example/?token=secret"}}}}
}

func TestProxyRestorePlanSafety(t *testing.T) {
	before := proxyTestSnapshot()
	applied := CloneProxySnapshot(before)
	applied.Entries[0].Values["mode"], applied.Entries[0].Values["server"] = "on", "127.0.0.1:17890"
	for _, name := range []string{"full", "partial", "already-restored", "external-owned", "external-unowned", "force", "other-scope", "missing-service", "missing-field"} {
		t.Run(name, func(t *testing.T) {
			current := CloneProxySnapshot(applied)
			force := name == "force"
			switch name {
			case "partial":
				current.Entries[0].Values["server"] = "old:80"
			case "already-restored":
				current = CloneProxySnapshot(before)
			case "external-owned", "force":
				current.Entries[0].Values["server"] = "other:88"
				current.Entries[0].Values["bypass"] = "external"
			case "external-unowned":
				current.Entries[0].Values["bypass"] = "external"
			case "other-scope":
				current.Scope = "user-b"
			case "missing-service":
				current.Entries = nil
			case "missing-field":
				delete(current.Entries[0].Values, "server")
			}
			plan, conflicts, err := ProxyRestorePlan(current, before, applied, force)
			if name == "other-scope" {
				if err == nil {
					t.Fatal("cross-user restore accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "external-owned" || name == "missing-service" || name == "missing-field" {
				if len(conflicts) != 1 || len(plan.Entries) != 0 {
					t.Fatal(plan, conflicts)
				}
				return
			}
			if len(conflicts) != 0 {
				t.Fatal(conflicts)
			}
			if name == "already-restored" {
				if len(plan.Entries) != 0 {
					t.Fatal(plan)
				}
				return
			}
			if len(plan.Entries) != 1 || plan.Entries[0].Values["server"] != "old:80" || plan.Entries[0].Values["mode"] != "off" {
				t.Fatal(plan)
			}
			if (name == "force" || name == "external-unowned") && plan.Entries[0].Values["bypass"] != "external" {
				t.Fatal("unowned changes overwritten")
			}
			plan.Entries[0].Values["pac"] = "mutated"
			if before.Entries[0].Values["pac"] == "mutated" || applied.Entries[0].Values["pac"] == "mutated" {
				t.Fatal("aliased snapshot")
			}
		})
	}
}

func TestRestoreIndependentNetworkServices(t *testing.T) {
	before := proxyTestSnapshot()
	before.Entries = append(before.Entries, ProxyEntry{Name: "ethernet", Values: map[string]string{"mode": "off", "server": "original"}})
	applied := CloneProxySnapshot(before)
	for i := range applied.Entries {
		applied.Entries[i].Values["mode"] = "on"
	}
	current := CloneProxySnapshot(applied)
	current.Entries[0].Values["mode"] = "external"
	plan, conflicts, err := ProxyRestorePlan(current, before, applied, false)
	if err != nil || len(conflicts) != 1 || len(plan.Entries) != 1 || plan.Entries[0].Name != "ethernet" {
		t.Fatal(plan, conflicts, err)
	}
}

func TestGNOMEProxyCommandsRoundTrip(t *testing.T) {
	values := map[string]string{}
	for _, key := range gnomeProxyKeys {
		values[key] = "''"
	}
	values[gnomeProxyKeys[0]] = "'auto'"
	values["org.gnome.system.proxy/ignore-hosts"] = "['localhost', '10.0.0.0/8']"
	values["org.gnome.system.proxy/use-same-proxy"] = "true"
	values["org.gnome.system.proxy.http/use-authentication"] = "true"
	for _, p := range []string{"http", "https"} {
		values["org.gnome.system.proxy."+p+"/port"] = "0"
	}
	var writes []string
	b := desktopProxyBackend{kind: "linux", scope: func() (string, error) { return "uid:session", nil }, run: func(_ context.Context, exe string, args ...string) (string, error) {
		if exe != "gsettings" || len(args) < 3 {
			t.Fatal(exe, args)
		}
		key := args[1] + "/" + args[2]
		switch args[0] {
		case "get":
			return values[key], nil
		case "writable":
			return "true", nil
		case "set":
			values[key] = args[3]
			writes = append(writes, key)
			return "", nil
		}
		return "", errors.New("unexpected command")
	}}
	ctx := context.Background()
	before, err := b.Capture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := b.Target(before, "127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Apply(ctx, target); err != nil {
		t.Fatal(err)
	}
	actual, _ := b.Capture(ctx)
	if !EqualProxySnapshots(actual, target) || b.Inspect(actual, "127.0.0.1:17890").State != "this_app" {
		t.Fatal(actual)
	}
	if writes[len(writes)-1] != gnomeProxyKeys[0] {
		t.Fatal("mode must be switched last", writes)
	}
	plan, conflicts, err := ProxyRestorePlan(actual, before, target, false)
	if err != nil || len(conflicts) > 0 {
		t.Fatal(err, conflicts)
	}
	if err = b.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	actual, _ = b.Capture(ctx)
	if !EqualProxySnapshots(actual, before) {
		t.Fatal(actual, before)
	}
	b.run = func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "get" {
			return values[args[1]+"/"+args[2]], nil
		}
		return "false", nil
	}
	if err = b.Apply(ctx, target); err == nil || !strings.Contains(err.Error(), "锁定") {
		t.Fatal("locked settings must fail", err)
	}
}

func TestMacOSCaptureTargetAndMixedServices(t *testing.T) {
	b := desktopProxyBackend{kind: "darwin", scope: func() (string, error) { return "501", nil }, run: func(_ context.Context, _ string, args ...string) (string, error) {
		switch args[0] {
		case "-listallnetworkservices":
			return "An asterisk (*) denotes disabled services.\nWi-Fi\n*Disabled\nEthernet", nil
		case "-getautoproxyurl":
			return "URL: https://private.example/pac?secret=1\nEnabled: No", nil
		case "-getproxyautodiscovery":
			return "Auto Proxy Discovery: Off", nil
		default:
			return "Enabled: No\nServer: \nPort: 0\nAuthenticated Proxy Enabled: 0", nil
		}
	}}
	before, err := b.Capture(context.Background())
	if err != nil || len(before.Entries) != 2 {
		t.Fatal(before, err)
	}
	target, err := b.Target(before, "127.0.0.1:17890")
	if err != nil || b.Inspect(target, "127.0.0.1:17890").State != "this_app" {
		t.Fatal(target, err)
	}
	if target.Entries[0].Values["pac.url"] != before.Entries[0].Values["pac.url"] {
		t.Fatal("PAC URL lost")
	}
	mixed := CloneProxySnapshot(before)
	mixed.Entries[1].Values["https.enabled"] = "Yes"
	mixed.Entries[1].Values["https.host"] = "company.proxy"
	mixed.Entries[1].Values["https.port"] = "80"
	if b.Inspect(mixed, "127.0.0.1:17890").State != "other" {
		t.Fatal("disabled service hid external proxy")
	}
	before.Entries[0].Values["http.auth"] = "1"
	if _, err = b.Target(before, "127.0.0.1:17890"); err == nil {
		t.Fatal("authenticated proxy must not be overwritten")
	}
}

func TestMacOSRestoreCommands(t *testing.T) {
	values := map[string]string{"http.enabled": "No", "http.host": "old", "http.port": "80", "http.auth": "0", "https.enabled": "No", "https.host": "old", "https.port": "80", "https.auth": "0", "socks.enabled": "No", "socks.host": "", "socks.port": "0", "socks.auth": "0", "pac.url": "https://private/pac", "pac.enabled": "Yes", "discovery": "On"}
	b := desktopProxyBackend{kind: "darwin", scope: func() (string, error) { return "501", nil }, run: func(_ context.Context, _ string, args ...string) (string, error) {
		flag := args[0]
		if flag == "-listallnetworkservices" {
			return "Wi-Fi", nil
		}
		if flag == "-getproxyautodiscovery" {
			return "Auto Proxy Discovery: " + values["discovery"], nil
		}
		if flag == "-getautoproxyurl" {
			return "URL: " + values["pac.url"] + "\nEnabled: " + values["pac.enabled"], nil
		}
		for _, p := range []struct{ key, get, set, toggle string }{{"http", "-getwebproxy", "-setwebproxy", "-setwebproxystate"}, {"https", "-getsecurewebproxy", "-setsecurewebproxy", "-setsecurewebproxystate"}, {"socks", "-getsocksfirewallproxy", "", ""}} {
			if flag == p.get {
				return "Enabled: " + values[p.key+".enabled"] + "\nServer: " + values[p.key+".host"] + "\nPort: " + values[p.key+".port"] + "\nAuthenticated Proxy Enabled: " + values[p.key+".auth"], nil
			}
			if flag == p.set {
				values[p.key+".host"], values[p.key+".port"], values[p.key+".enabled"] = args[2], args[3], "Yes"
				return "", nil
			}
			if flag == p.toggle {
				values[p.key+".enabled"] = "No"
				if args[2] == "on" {
					values[p.key+".enabled"] = "Yes"
				}
				return "", nil
			}
		}
		if flag == "-setautoproxystate" {
			values["pac.enabled"] = "No"
			if args[2] == "on" {
				values["pac.enabled"] = "Yes"
			}
			return "", nil
		}
		if flag == "-setproxyautodiscovery" {
			values["discovery"] = "Off"
			if args[2] == "on" {
				values["discovery"] = "On"
			}
			return "", nil
		}
		return "", errors.New("unexpected command")
	}}
	ctx := context.Background()
	before, _ := b.Capture(ctx)
	target, _ := b.Target(before, "127.0.0.1:17890")
	if err := b.Apply(ctx, target); err != nil {
		t.Fatal(err)
	}
	actual, _ := b.Capture(ctx)
	if !reflect.DeepEqual(actual, target) {
		t.Fatal(actual, target)
	}
	plan, _, _ := ProxyRestorePlan(actual, before, target, false)
	if err := b.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	actual, _ = b.Capture(ctx)
	if !EqualProxySnapshots(actual, before) {
		t.Fatal("restore lost state", actual, before)
	}
}
