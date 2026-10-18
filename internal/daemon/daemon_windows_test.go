package daemon

import (
	"strings"
	"testing"
)

func TestScripts(t *testing.T) {
	t.Setenv("ProgramFiles", `C:\Program Files`)
	installed := installScript(`C:\Program Files\Connaio\connaio-core.exe`, `C:\Temp\stage`)
	for _, want := range []string{"sc create " + Service, "sc config " + Service, "-ext-ctl-pipe " + Socket, `\"C:\Program Files\Connaio\connaio-core.exe\" -d`, "sc start " + Service, `del /F /Q "` + Helper + `"`} {
		if !strings.Contains(installed, want) {
			t.Errorf("installed core's script lacks %q:\n%s", want, installed)
		}
	}
	if strings.Contains(installed, "copy /Y \"C:\\Program Files") {
		t.Errorf("an installed core is run in place, not copied:\n%s", installed)
	}
	elsewhere := installScript(`C:\Users\me\Connaio\connaio-core.exe`, `C:\Temp\stage`)
	for _, want := range []string{`copy /Y "C:\Users\me\Connaio\connaio-core.exe" "` + Helper + `"`, `\"` + Helper + `\" -d`} {
		if !strings.Contains(elsewhere, want) {
			t.Errorf("a core outside Program Files is copied first; script lacks %q:\n%s", want, elsewhere)
		}
	}
	if u := uninstallScript(); !strings.Contains(u, "sc delete "+Service) {
		t.Errorf("uninstall script:\n%s", u)
	}
}
