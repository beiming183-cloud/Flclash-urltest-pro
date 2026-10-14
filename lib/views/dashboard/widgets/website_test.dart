import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/common/website_probe.dart';
import 'package:fl_clash/widgets/widgets.dart';
import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

class _Website {
  final String name;
  final String url;
  final String? logo;
  final Color color;

  const _Website(this.name, this.url, this.logo, this.color);
}

class WebsiteTestPanel extends StatefulWidget {
  final bool connected;
  final int port;
  final WebsiteProbe probe;

  const WebsiteTestPanel({
    super.key,
    required this.connected,
    required this.port,
    this.probe = probeWebsite,
  });

  @override
  State<WebsiteTestPanel> createState() => _WebsiteTestPanelState();
}

class _WebsiteTestPanelState extends State<WebsiteTestPanel> {
  final _sites = <_Website>[
    const _Website('Apple', 'https://www.apple.com/library/test/success.html', 'apple', Colors.black),
    const _Website('GitHub', 'https://github.com', 'github', Colors.black),
    const _Website('Google', 'https://www.google.com/generate_204', 'google', Color(0xff4285f4)),
    const _Website('YouTube', 'https://www.youtube.com/generate_204', 'youtube', Color(0xffff0033)),
  ];
  final _results = <String, WebsiteProbeResult>{};
  final _pending = <String>{};
  late final _revision = ValueNotifier<int>(0);
  int _epoch = 0;

  void _notify() {
    if (mounted) {
      setState(() {});
      _revision.value++;
    }
  }

  @override
  void didUpdateWidget(WebsiteTestPanel oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.connected != widget.connected || oldWidget.port != widget.port) {
      _epoch++;
      _pending.clear();
      _results.clear();
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _revision.value++;
      });
    }
  }

  @override
  void dispose() {
    _epoch++;
    _revision.dispose();
    super.dispose();
  }

  Future<void> _test(_Website site) async {
    if (!widget.connected || _pending.contains(site.url)) return;
    final epoch = _epoch;
    _pending.add(site.url);
    _results.remove(site.url);
    _notify();
    WebsiteProbeResult result;
    try {
      result = await widget.probe(Uri.parse(site.url), widget.port);
    } catch (_) {
      result = const WebsiteProbeResult(WebsiteProbeState.failed);
    }
    if (!mounted || epoch != _epoch) return;
    _pending.remove(site.url);
    _results[site.url] = result;
    _notify();
  }

  Future<void> _testAll() async {
    if (_pending.isNotEmpty || !widget.connected) return;
    await Future.wait(_sites.toList().map(_test));
  }

  Future<void> _addWebsite(BuildContext dialogContext) async {
    final controller = TextEditingController(text: 'https://');
    String? error;
    final url = await showDialog<String>(
      context: dialogContext,
      builder: (context) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: Text(context.appLocalizations.websiteTestAdd),
          content: TextField(
            controller: controller,
            autofocus: true,
            keyboardType: TextInputType.url,
            decoration: InputDecoration(
              labelText: context.appLocalizations.websiteTestUrl,
              errorText: error,
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: Text(context.appLocalizations.cancel),
            ),
            FilledButton(
              onPressed: () {
                final uri = Uri.tryParse(controller.text.trim());
                if (uri == null || uri.scheme != 'https' || uri.host.isEmpty || uri.userInfo.isNotEmpty) {
                  setDialogState(() => error = context.appLocalizations.websiteTestInvalidUrl);
                  return;
                }
                Navigator.pop(context, uri.toString());
              },
              child: Text(context.appLocalizations.add),
            ),
          ],
        ),
      ),
    );
    if (mounted && url != null && !_sites.any((site) => site.url == url)) {
      _sites.add(_Website(Uri.parse(url).host, url, null, Colors.blue));
      _notify();
    }
  }

  Future<void> _openFloatingPanel() async {
    await showDialog<void>(
      context: context,
      builder: (context) => Dialog(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 660),
          child: ValueListenableBuilder(
            valueListenable: _revision,
            builder: (context, value, child) => SingleChildScrollView(
              child: _content(context, floating: true),
            ),
          ),
        ),
      ),
    );
  }

  String _label(BuildContext context, _Website site) {
    if (!widget.connected) return context.appLocalizations.websiteTestOffline;
    final result = _results[site.url];
    return switch (result?.state) {
      WebsiteProbeState.success => '${result!.milliseconds} ms',
      WebsiteProbeState.timeout => context.appLocalizations.timeout,
      WebsiteProbeState.failed => context.appLocalizations.websiteTestFailed,
      WebsiteProbeState.httpError => 'HTTP ${result!.statusCode}',
      null => context.appLocalizations.websiteTest,
    };
  }

  Widget _tile(BuildContext context, _Website site, double width) {
    final pending = _pending.contains(site.url);
    final result = _results[site.url];
    final failed = result != null && result.state != WebsiteProbeState.success;
    final foreground = failed ? context.colorScheme.error : context.colorScheme.primary;
    return SizedBox(
      width: width,
      child: Material(
        color: context.colorScheme.primaryContainer.withValues(alpha: 0.24),
        borderRadius: BorderRadius.circular(18),
        child: InkWell(
          borderRadius: BorderRadius.circular(18),
          onTap: widget.connected && !pending ? () => _test(site) : null,
          child: Padding(
            padding: const EdgeInsets.all(14),
            child: Column(
              children: [
                site.logo == null
                    ? Icon(Icons.language, size: 36, color: site.color)
                    : SvgPicture.asset(
                        'assets/images/icon/website_${site.logo}.svg',
                        width: 36,
                        height: 36,
                        colorFilter: ColorFilter.mode(
                          context.colorScheme.brightness == Brightness.dark && site.color == Colors.black
                              ? Colors.white
                              : site.color,
                          BlendMode.srcIn,
                        ),
                      ),
                const SizedBox(height: 8),
                Text(site.name, maxLines: 1, overflow: TextOverflow.ellipsis, style: context.textTheme.titleMedium),
                const Padding(padding: EdgeInsets.symmetric(vertical: 6), child: Divider()),
                SizedBox(
                  height: 24,
                  child: pending
                      ? const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2))
                      : Text(
                          _label(context, site),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: context.textTheme.labelLarge?.copyWith(color: foreground),
                        ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _content(BuildContext context, {bool floating = false}) {
    final labels = context.appLocalizations;
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.network_check, color: context.colorScheme.primary),
              const SizedBox(width: 10),
              Expanded(child: Text(labels.websiteTests, style: context.textTheme.titleMedium)),
              IconButton(
                tooltip: labels.websiteTestAll,
                onPressed: widget.connected && _pending.isEmpty ? _testAll : null,
                icon: const Icon(Icons.speed),
              ),
              IconButton(
                tooltip: labels.websiteTestAdd,
                onPressed: () => _addWebsite(context),
                icon: const Icon(Icons.add),
              ),
              IconButton(
                tooltip: floating ? labels.cancel : labels.websiteTests,
                onPressed: floating ? () => Navigator.pop(context) : _openFloatingPanel,
                icon: Icon(floating ? Icons.close : Icons.open_in_full),
              ),
            ],
          ),
          Text(labels.websiteTestCurrentRoute, style: context.textTheme.bodySmall?.copyWith(color: context.colorScheme.onSurfaceVariant)),
          const SizedBox(height: 16),
          LayoutBuilder(
            builder: (context, constraints) {
              final columns = constraints.maxWidth >= 540 ? 4 : 2;
              final width = (constraints.maxWidth - 12 * (columns - 1)) / columns;
              return Wrap(
                spacing: 12,
                runSpacing: 12,
                children: _sites.map((site) => _tile(context, site, width)).toList(),
              );
            },
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) => CommonCard(child: _content(context));
}
