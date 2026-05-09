// stop.exe — kill the cluster-installer app and any leftover dev-server
// listener. Safe to run when nothing is up; it just reports "no targets".
using System;
using System.Diagnostics;
using System.IO;

class Stop {
    const int WailsDevPort = 34115; // legacy dev-mode listener — usually unused

    static int Main() {
        bool killedAny = false;

        // 1) The main app process by name (production binary).
        if (RunCapture("taskkill", "/IM cluster-installer.exe /T /F") == 0) {
            Console.WriteLine("killed cluster-installer.exe");
            killedAny = true;
        }

        // 2) Anything still listening on the wails dev port (defensive — only
        //    matters if someone is running 'wails dev' directly).
        string pid = PidOnPort(WailsDevPort);
        if (!string.IsNullOrEmpty(pid)) {
            if (RunCapture("taskkill", "/PID " + pid + " /T /F") == 0) {
                Console.WriteLine("killed PID " + pid + " (was on :" + WailsDevPort + ")");
                killedAny = true;
            }
        }

        // 3) Wails CLI itself, if it was started by build/start.
        if (RunCapture("taskkill", "/IM wails.exe /T /F") == 0) {
            Console.WriteLine("killed wails.exe");
            killedAny = true;
        }

        if (!killedAny) {
            Console.WriteLine("nothing to stop.");
        }
        return 0;
    }

    static string PidOnPort(int port) {
        string script = "Get-NetTCPConnection -LocalPort " + port + " -State Listen "
                      + "-ErrorAction SilentlyContinue | "
                      + "Select-Object -First 1 -ExpandProperty OwningProcess";
        var psi = new ProcessStartInfo("powershell.exe",
            "-NoProfile -Command \"" + script + "\"") {
            UseShellExecute        = false,
            RedirectStandardOutput = true,
            RedirectStandardError  = true,
            CreateNoWindow         = true
        };
        try {
            using (var p = Process.Start(psi)) {
                string outp = p.StandardOutput.ReadToEnd();
                p.WaitForExit();
                return outp.Trim();
            }
        } catch { return ""; }
    }

    static int RunCapture(string file, string args) {
        var psi = new ProcessStartInfo(file, args) {
            UseShellExecute        = false,
            RedirectStandardOutput = true,
            RedirectStandardError  = true,
            CreateNoWindow         = true
        };
        try {
            using (var p = Process.Start(psi)) {
                p.StandardOutput.ReadToEnd();
                p.StandardError.ReadToEnd();
                p.WaitForExit();
                return p.ExitCode;
            }
        } catch { return -1; }
    }
}
