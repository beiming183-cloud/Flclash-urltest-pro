import 'dart:async';

import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/core/core.dart';
import 'package:fl_clash/widgets/widgets.dart';
import 'package:flutter/material.dart';

class SelectionLogView extends StatefulWidget {
  const SelectionLogView({super.key});

  @override
  State<SelectionLogView> createState() => _SelectionLogViewState();
}

class _SelectionLogViewState extends State<SelectionLogView> {
  Timer? _timer;
  List<dynamic> _groups = [];
  bool _reading = false;
  bool _loaded = false;
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    unawaited(_refresh());
    _timer = Timer.periodic(const Duration(seconds: 3), (_) => unawaited(_refresh()));
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _refresh() async {
    if (_reading) return;
    _reading = true;
    try {
      final groups = await coreController.getStabilityState();
      if (mounted) {
        setState(() {
          _groups = groups;
          _loaded = true;
          _failed = false;
        });
      }
    } catch (_) {
      if (mounted) setState(() => _failed = true);
    } finally {
      _reading = false;
    }
  }

  @override
  Widget build(BuildContext context) {
    final labels = context.appLocalizations;
    final entries = <({Map event, Map names, String group})>[];
    for (final group in _groups.cast<Map>()) {
      final state = group['state'] as Map? ?? {};
      final names = group['names'] as Map? ?? {};
      for (final event in (state['selections'] as List? ?? []).reversed.cast<Map>()) {
        entries.add((event: event, names: names, group: group['group'] as String? ?? ''));
      }
    }
    entries.sort((a, b) => (b.event['time'] as String? ?? '').compareTo(a.event['time'] as String? ?? ''));
    return CommonScaffold(
      title: labels.stabilitySelectionLog,
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Text(labels.stabilitySelectionLogDescription),
          const SizedBox(height: 12),
          if (_failed) Text(labels.stabilityUnavailable),
          if (!_loaded && !_failed) const Center(child: CircularProgressIndicator()),
          if (_loaded && entries.isEmpty) Text(labels.stabilitySelectionLogEmpty),
          for (final entry in entries.take(128))
            Card(child: SelectionLogEntry(event: entry.event, names: entry.names, group: entry.group)),
        ],
      ),
    );
  }
}

class SelectionLogEntry extends StatelessWidget {
  const SelectionLogEntry({super.key, required this.event, required this.names, required this.group});

  final Map<dynamic, dynamic> event;
  final Map<dynamic, dynamic> names;
  final String group;

  @override
  Widget build(BuildContext context) {
    final labels = context.appLocalizations;
    final node = names[event['node']] as String? ?? labels.stabilitySelectionUnknownNode;
    final previous = names[event['previous']] as String?;
    final kind = event['kind'];
    final title = switch (kind) {
      'primary' => previous == null ? labels.stabilitySelectionPrimary(node) : labels.stabilitySelectionSwitch(previous, node),
      'backup' => labels.stabilitySelectionBackup(node),
      'removed' => labels.stabilitySelectionRemoved(node),
      _ => labels.stabilitySelectionRejected(node),
    };
    final reason = switch (event['reason']) {
      'latency_quality_improved' => labels.stabilitySelectionBetter,
      'throughput_improved' => labels.stabilitySelectionThroughput,
      'manual' => labels.stabilitySelectionManual,
      'dial_failover' => labels.stabilitySelectionFailover,
      'https_timeout' || 'relay_dial_timeout' || 'exit_query_timeout' || 'tls_handshake_timeout' => labels.stabilitySelectionTimeout,
      'unexpected_exit' => labels.stabilityWrongExit,
      'cert_untrusted' || 'cert_hostname' || 'cert_expired' => labels.stabilityCertificate,
      'pool_replaced' => labels.stabilitySelectionReordered,
      'health_check_expired' => labels.stabilitySelectionExpired,
      'pool_selected' || 'promote' => labels.stabilitySelectionVerified,
      _ => labels.stabilitySelectionFailed,
    };
    final at = DateTime.tryParse(event['time'] as String? ?? '')?.toLocal();
    final time = at == null ? '' : '${at.hour.toString().padLeft(2, '0')}:${at.minute.toString().padLeft(2, '0')}:${at.second.toString().padLeft(2, '0')}';
    final delay = (event['requestLatencyMs'] as num? ?? 0).round();
    final jitter = (event['jitterMs'] as num? ?? 0).round();
    return ListTile(
      leading: Icon(kind == 'removed' || kind == 'rejected' ? Icons.remove_circle_outline : Icons.check_circle_outline,
        color: kind == 'removed' || kind == 'rejected' ? context.colorScheme.error : context.colorScheme.primary),
      title: Text(title),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(reason),
          if (delay > 0) Text(labels.stabilityRequestLatency(delay, jitter)),
          Text('$time · $group', style: context.textTheme.bodySmall),
        ],
      ),
    );
  }
}
