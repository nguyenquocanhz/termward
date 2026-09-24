import Capacitor
import Foundation
import Security
import Termwardcore
import UserNotifications

/// iOS bridge used by app/src/lib/mobile.ts: starts the Go core inside the app
/// and posts its health alerts as local notifications.
@objc(TermwardCorePlugin)
public class TermwardCorePlugin: CAPPlugin, CAPBridgedPlugin, NotificationHandlerProtocol {
    public let identifier = "TermwardCorePlugin"
    public let jsName = "TermwardCore"
    public let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "start", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "setLanguage", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "getBackground", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "setBackground", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "requestPermissions", returnType: CAPPluginReturnPromise),
    ]

    private static var port = 0
    private static var token = ""
    private let notifier = AlertNotifier()

    override public func load() {
        // Receive taps on our alert notifications.
        bridge?.notificationRouter.localNotificationHandler = self
    }

    @objc func start(_ call: CAPPluginCall) {
        let language = call.getString("language") ?? "en"
        DispatchQueue.global(qos: .userInitiated).async {
            do {
                try TermwardCorePlugin.ensureStarted(language: language, notifier: self.notifier)
                call.resolve(["port": TermwardCorePlugin.port, "token": TermwardCorePlugin.token])
            } catch {
                call.reject("Termward core failed to start: \(error.localizedDescription)")
            }
        }
    }

    private static func ensureStarted(language: String, notifier: AlertNotifier) throws {
        if port != 0 { return }
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("core", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        // Keep the data out of iCloud/iTunes backups: it holds private keys.
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        var d = dir
        try? d.setResourceValues(values)

        var p = 0
        try MobileStart(dir.path, try VaultKey.get(), language, notifier, &p)
        port = p
        token = MobileToken()
    }

    @objc func setLanguage(_ call: CAPPluginCall) {
        MobileSetLanguage(call.getString("language") ?? "en")
        call.resolve()
    }

    // iOS suspends apps in the background, so there is no background mode.
    @objc func getBackground(_ call: CAPPluginCall) {
        call.resolve(["enabled": false])
    }

    @objc func setBackground(_ call: CAPPluginCall) {
        call.reject("Background monitoring is not available on iOS")
    }

    @objc override public func requestPermissions(_ call: CAPPluginCall) {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { granted, _ in
            call.resolve(["notifications": granted ? "granted" : "denied"])
        }
    }

    // MARK: NotificationHandlerProtocol

    public func willPresent(notification: UNNotification) -> UNNotificationPresentationOptions {
        return [.banner, .list, .sound]
    }

    public func didReceive(response: UNNotificationResponse) {
        if let hostId = response.notification.request.content.userInfo["hostId"] as? String {
            notifyListeners("notificationTap", data: ["hostId": hostId], retainUntilConsumed: true)
        }
    }
}

/// Called by the Go core for every health alert.
final class AlertNotifier: NSObject, MobileNotifierProtocol {
    func post(_ title: String?, body: String?, hostID: String?) {
        let content = UNMutableNotificationContent()
        content.title = title ?? "Termward"
        content.body = body ?? ""
        content.sound = .default
        content.userInfo = ["hostId": hostID ?? ""]
        // One notification per server: a newer state replaces the older one.
        let request = UNNotificationRequest(identifier: "host-\(hostID ?? "")", content: content, trigger: nil)
        UNUserNotificationCenter.current().add(request)
    }
}

/// 32-byte key that encrypts remembered passwords, kept in the Keychain on
/// this device only (never synced).
enum VaultKey {
    private static let service = "io.github.nguyenquocanhz.termward"
    private static let account = "vault"

    static func get() throws -> String {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
        ]
        var out: AnyObject?
        if SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess, let data = out as? Data {
            return data.map { String(format: "%02x", $0) }.joined()
        }
        var bytes = [UInt8](repeating: 0, count: 32)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else {
            throw NSError(domain: "Termward", code: 1, userInfo: [NSLocalizedDescriptionKey: "no randomness"])
        }
        let add: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            kSecValueData as String: Data(bytes),
        ]
        let status = SecItemAdd(add as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw NSError(domain: "Termward", code: Int(status), userInfo: [NSLocalizedDescriptionKey: "keychain error \(status)"])
        }
        return bytes.map { String(format: "%02x", $0) }.joined()
    }
}
