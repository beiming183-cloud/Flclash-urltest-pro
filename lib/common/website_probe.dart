import 'dart:async';
import 'dart:io';

enum WebsiteProbeState { success, timeout, failed, httpError }

class WebsiteProbeResult {
  final WebsiteProbeState state;
  final int? milliseconds;
  final int? statusCode;

  const WebsiteProbeResult(this.state, {this.milliseconds, this.statusCode});
}

typedef WebsiteProbe = Future<WebsiteProbeResult> Function(Uri uri, int port);

Future<WebsiteProbeResult> probeWebsite(Uri uri, int port) async {
  if (uri.scheme != 'https' || uri.host.isEmpty || port < 1 || port > 65535) {
    return const WebsiteProbeResult(WebsiteProbeState.failed);
  }
  final client = HttpClient();
  client.findProxy = (_) => 'PROXY 127.0.0.1:$port';
  client.connectionTimeout = const Duration(seconds: 5);
  final stopwatch = Stopwatch()..start();
  try {
    return await (() async {
      final request = await client.getUrl(uri);
      request.followRedirects = false;
      request.headers.set(HttpHeaders.cacheControlHeader, 'no-cache');
      final response = await request.close();
      final status = response.statusCode;
      return WebsiteProbeResult(
        status >= 200 && status < 400
            ? WebsiteProbeState.success
            : WebsiteProbeState.httpError,
        milliseconds: stopwatch.elapsedMilliseconds,
        statusCode: status,
      );
    })().timeout(const Duration(seconds: 5));
  } on TimeoutException {
    return const WebsiteProbeResult(WebsiteProbeState.timeout);
  } catch (_) {
    return const WebsiteProbeResult(WebsiteProbeState.failed);
  } finally {
    stopwatch.stop();
    client.close(force: true);
  }
}
