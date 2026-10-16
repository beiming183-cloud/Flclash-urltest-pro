import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/common/theme.dart';
import 'package:fl_clash/l10n/l10n.dart';
import 'package:fl_clash/state.dart';
import 'package:fl_clash/views/selection_log.dart';
import 'package:fl_clash/views/stability.dart';
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('short speed samples are references and pending stability is visible', (tester) async {
    await tester.pumpWidget(_TestApp(child: StabilityNodeMetrics(node: {
      'ewmaMs': 200,
      'throughputMbps': 24.6,
      'throughputReliable': false,
      'throughputAt': DateTime.now().toUtc().toIso8601String(),
      'qualityStreak': 1,
    })));
    await tester.pumpAndSettle();
    expect(find.text('Short download reference 24.6 Mbps · excluded from ranking'), findsOneWidget);
    expect(find.text('Available · gathering stability evidence'), findsOneWidget);
    expect(tester.takeException(), null);
  });

  testWidgets('request delay and first-response measurement are separate', (tester) async {
    await tester.pumpWidget(const _TestApp(child: StabilityNodeMetrics(node: {
      'ewmaMs': 200,
      'jitterMs': 15,
      'firstResponseMs': 3500,
      'firstResponseP95Ms': 3800,
    })));
    await tester.pumpAndSettle();
    expect(find.text('200 ms request latency · 15 ms variation'), findsOneWidget);
    expect(find.text('First response (incl. setup) 3500 ms · P95 3800 ms'), findsOneWidget);
    expect(tester.takeException(), null);
  });

  testWidgets('missing first-response telemetry is explicitly unknown', (tester) async {
    await tester.pumpWidget(const _TestApp(child: StabilityNodeMetrics(node: {'ewmaMs': 220})));
    await tester.pumpAndSettle();
    expect(find.text('First response (incl. setup): awaiting measurement'), findsOneWidget);
  });

  testWidgets('selection entry resolves names and shows a plain reason', (tester) async {
    await tester.pumpWidget(const _TestApp(child: SelectionLogEntry(
      group: 'IP1',
      names: {'a': 'Old node', 'b': 'Fast node'},
      event: {
        'kind': 'primary',
        'node': 'b',
        'previous': 'a',
        'reason': 'latency_quality_improved',
        'time': '2026-10-14T10:00:00Z',
        'requestLatencyMs': 200,
        'jitterMs': 12,
        'firstResponseMs': 3000,
      },
    )));
    await tester.pumpAndSettle();
    expect(find.text('Primary switched · Old node → Fast node'), findsOneWidget);
    expect(find.text('Better low-latency stability score'), findsOneWidget);
    expect(find.text('200 ms request latency · 12 ms variation'), findsOneWidget);
    expect(find.textContaining('3000'), findsNothing);
  });

  testWidgets('timeout removal stays readable on a narrow Chinese screen', (tester) async {
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(const _TestApp(locale: Locale('zh', 'CN'), child: SelectionLogEntry(
      group: 'IP1',
      names: {'a': 'IP1-测试节点-001'},
      event: {'kind': 'removed', 'node': 'a', 'reason': 'https_timeout', 'time': '2026-10-14T10:00:00Z'},
    )));
    await tester.pumpAndSettle();
    expect(find.text('移出候选 · IP1-测试节点-001'), findsOneWidget);
    expect(find.text('连接或校验超时'), findsOneWidget);
    expect(tester.takeException(), null);
  });
}

class _TestApp extends StatelessWidget {
  const _TestApp({required this.child, this.locale = const Locale('en')});

  final Widget child;
  final Locale locale;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      locale: locale,
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      supportedLocales: AppLocalizations.delegate.supportedLocales,
      builder: (context, child) {
        globalState.measure = Measure.of(context, 1);
        globalState.theme = CommonTheme.of(context, 1);
        return child!;
      },
      home: Scaffold(body: child),
    );
  }
}
