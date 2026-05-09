// start.exe — launch cluster-installer.exe (the self-contained Wails app).
// If cluster-installer.exe is missing, suggest running build.exe first.
using System;
using System.Diagnostics;
using System.IO;
using System.Reflection;

class Start {
    static int Main() {
        string exe  = Assembly.GetExecutingAssembly().Location;
        string root = Path.GetDirectoryName(exe);
        string app  = Path.Combine(root, "cluster-installer.exe");
        if (!File.Exists(app)) {
            Console.Error.WriteLine("start: cluster-installer.exe not found at " + app);
            Console.Error.WriteLine("       Run build.exe first to produce it.");
            return 1;
        }
        var psi = new ProcessStartInfo(app) {
            UseShellExecute  = true,
            WorkingDirectory = root
        };
        try {
            Process.Start(psi);
        } catch (Exception e) {
            Console.Error.WriteLine("start: " + e.Message);
            return 1;
        }
        return 0;
    }
}
