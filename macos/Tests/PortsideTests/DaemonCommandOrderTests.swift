import Foundation
import Testing
@testable import Portside

/// Daemon commands and helper calls: order and cancellation.
@MainActor
struct DaemonCommandOrderTests {
    /// A later command must not overtake an earlier, slower one (a Stop
    /// clicked after a Restart has to run after it).
    @Test func commandsRunInTheOrderTheyWereAdded() async {
        let jobs = SerialJobs()
        var order: [Int] = []
        for (index, delay) in [80, 40, 0].enumerated() {
            jobs.add {
                try? await Task.sleep(for: .milliseconds(delay))
                order.append(index)
            }
        }
        await withCheckedContinuation { cont in
            jobs.add { cont.resume() }
        }
        #expect(order == [0, 1, 2])
    }

    /// A command waits for the connection loop that was running when it was added.
    @Test func commandWaitsForTheRunningLoop() async {
        let jobs = SerialJobs()
        var events: [String] = []
        let loop = Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(50))
            events.append("loop ended")
        }
        await withCheckedContinuation { cont in
            jobs.add(after: loop) {
                events.append("command")
                cont.resume()
            }
        }
        #expect(events == ["loop ended", "command"])
    }
}

/// Helper calls that are cancelled must not run their command (for example
/// start a daemon the user has just stopped).
struct HelperCancellationTests {
    /// A script that writes a marker file after a short delay.
    private func helper(in dir: URL) throws -> (script: URL, marker: URL) {
        let script = dir.appendingPathComponent("helper.sh")
        let marker = dir.appendingPathComponent("ran")
        try "#!/bin/sh\nsleep 0.3\ntouch \"$1\"\n".write(to: script, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)
        return (script, marker)
    }

    private func tempDir() throws -> URL {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    @Test func callCancelledBeforeLaunchDoesNotStartTheHelper() async throws {
        let dir = try tempDir()
        defer { try? FileManager.default.removeItem(at: dir) }
        let (script, marker) = try helper(in: dir)
        let call = Task {
            withUnsafeCurrentTask { $0?.cancel() }
            return try await DaemonCLI.exec(helper: script, [marker.path])
        }
        await #expect(throws: CancellationError.self) { try await call.value }
        try await Task.sleep(for: .milliseconds(600))
        #expect(!FileManager.default.fileExists(atPath: marker.path))
    }

    @Test func callCancelledWhileRunningTerminatesTheHelper() async throws {
        let dir = try tempDir()
        defer { try? FileManager.default.removeItem(at: dir) }
        let (script, marker) = try helper(in: dir)
        let call = Task { try await DaemonCLI.exec(helper: script, [marker.path]) }
        try await Task.sleep(for: .milliseconds(100))
        call.cancel()
        let out = try await call.value
        #expect(out.status != 0)
        try await Task.sleep(for: .milliseconds(600))
        #expect(!FileManager.default.fileExists(atPath: marker.path))
    }

    @Test func uncancelledCallRunsTheHelper() async throws {
        let dir = try tempDir()
        defer { try? FileManager.default.removeItem(at: dir) }
        let (script, marker) = try helper(in: dir)
        let out = try await DaemonCLI.exec(helper: script, [marker.path])
        #expect(out.status == 0)
        #expect(FileManager.default.fileExists(atPath: marker.path))
    }
}
