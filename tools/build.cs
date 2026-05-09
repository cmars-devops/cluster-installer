// build.exe — opens a console window running tools\build.ps1 so the user
// sees download/build progress. The PS script handles the actual work
// (portable Go bootstrap + Wails CLI install + wails build).
using System;
using System.Diagnostics;
using System.IO;
using System.Reflection;

class Build {
    static int Main() {
        string exe   = Assembly.GetExecutingAssembly().Location;
        string root  = Path.GetDirectoryName(exe);
        string ps1   = Path.Combine(root, "tools", "build.ps1");
        if (!File.Exists(ps1)) {
            Console.Error.WriteLine("build: tools\\build.ps1 not found at " + ps1);
            return 1;
        }
        // Open new console window with title; -NoExit keeps it open after
        // build completes so the user can read final messages.
        string args = "/C start \"Cluster Installer Build\" "
                    + "powershell.exe -NoExit -ExecutionPolicy Bypass -File \"" + ps1 + "\"";
        var psi = new ProcessStartInfo("cmd.exe", args) {
            UseShellExecute = false,
            CreateNoWindow  = false
        };
        try {
            Process.Start(psi);
        } catch (Exception e) {
            Console.Error.WriteLine("build: " + e.Message);
            return 1;
        }
        Console.WriteLine("Build started in a new console window.");
        return 0;
    }
}
