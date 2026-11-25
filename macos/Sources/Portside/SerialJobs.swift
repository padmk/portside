/// Runs asynchronous jobs one at a time, in the order they were added.
@MainActor
final class SerialJobs {
    private var last: Task<Void, Never>?

    /// Adds a job that starts once `first` and every job added before it have ended.
    func add(after first: Task<Void, Never>? = nil, _ job: @escaping @MainActor () async -> Void) {
        let previous = last
        last = Task {
            await first?.value
            await previous?.value
            await job()
        }
    }
}
