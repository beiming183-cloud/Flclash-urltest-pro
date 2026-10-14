import 'dart:async';

import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/common/theme.dart';
import 'package:fl_clash/common/website_probe.dart';
import 'package:fl_clash/l10n/l10n.dart';
import 'package:fl_clash/state.dart';
import 'package:fl_clash/views/dashboard/widgets/website_test.dart';
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('custom website rejects HTTP and accepts HTTPS', (tester) async {
    await tester.pumpWidget(const _TestApp(child: WebsiteTestPanel(
      connected: false,
      port: 7890,
    )));
    await tester.tap(find.byTooltip('Add website'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'http://example.com');
    await tester.tap(find.text('Add'));
    await tester.pumpAndSettle();
    expect(find.text('Enter a valid HTTPS URL'), findsOneWidget);
    await tester.enterText(find.byType(TextField), 'https://example.com');
    await tester.tap(find.text('Add'));
    await tester.pumpAndSettle();
    expect(find.text('example.com'), findsOneWidget);
    expect(tester.takeException(), null);
  });

  testWidgets('website panel tests each site through the supplied proxy port', (tester) async {
    final calls = <(Uri, int)>[];
    await tester.pumpWidget(_TestApp(child: WebsiteTestPanel(
      connected: true,
      port: 7897,
      probe: (uri, port) async {
        calls.add((uri, port));
        return const WebsiteProbeResult(WebsiteProbeState.success, milliseconds: 212);
      },
    )));
    await tester.tap(find.byTooltip('Test all'));
    await tester.pumpAndSettle();
    expect(calls.length, 4);
    expect(calls.every((call) => call.$1.scheme == 'https' && call.$2 == 7897), true);
    expect(find.text('212 ms'), findsNWidgets(4));
    expect(tester.takeException(), null);
  });

  testWidgets('disconnected panel does not start requests', (tester) async {
    var calls = 0;
    await tester.pumpWidget(_TestApp(child: WebsiteTestPanel(
      connected: false,
      port: 7890,
      probe: (uri, port) async {
        calls++;
        return const WebsiteProbeResult(WebsiteProbeState.success);
      },
    )));
    await tester.tap(find.text('Apple'));
    await tester.pump();
    expect(calls, 0);
    expect(find.text('Connect first'), findsNWidgets(4));
  });

  testWidgets('exception clears pending state and permits retry', (tester) async {
    var calls = 0;
    await tester.pumpWidget(_TestApp(child: WebsiteTestPanel(
      connected: true,
      port: 7890,
      probe: (uri, port) async {
        calls++;
        throw StateError('probe failed');
      },
    )));
    await tester.tap(find.text('Apple'));
    await tester.pumpAndSettle();
    expect(find.text('Failed'), findsOneWidget);
    expect(find.byType(CircularProgressIndicator), findsNothing);
    await tester.tap(find.text('Apple'));
    await tester.pumpAndSettle();
    expect(calls, 2);
  });

  testWidgets('floating panel shares results and prevents duplicate taps', (tester) async {
    final completer = Completer<WebsiteProbeResult>();
    var calls = 0;
    await tester.pumpWidget(_TestApp(child: WebsiteTestPanel(
      connected: true,
      port: 7890,
      probe: (uri, port) {
        calls++;
        return completer.future;
      },
    )));
    await tester.tap(find.byIcon(Icons.open_in_full));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Apple').last);
    await tester.pump();
    await tester.tap(find.text('Apple').last);
    expect(calls, 1);
    completer.complete(const WebsiteProbeResult(WebsiteProbeState.timeout));
    await tester.pumpAndSettle();
    expect(find.text('Timeout'), findsNWidgets(2));
    await tester.tap(find.byIcon(Icons.close));
    await tester.pumpAndSettle();
    expect(find.text('Timeout'), findsOneWidget);
  });

  testWidgets('mobile layout fits and late completion is safe after disposal', (tester) async {
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final completer = Completer<WebsiteProbeResult>();
    await tester.pumpWidget(_TestApp(child: WebsiteTestPanel(
      connected: true,
      port: 7890,
      probe: (uri, port) => completer.future,
    )));
    expect(tester.takeException(), null);
    await tester.tap(find.text('Apple'));
    await tester.pumpWidget(const SizedBox());
    completer.complete(const WebsiteProbeResult(WebsiteProbeState.success, milliseconds: 200));
    await tester.pump();
    expect(tester.takeException(), null);
  });
}

class _TestApp extends StatelessWidget {
  final Widget child;

  const _TestApp({required this.child});

  @override
  Widget build(BuildContext context) => MaterialApp(
    locale: const Locale('en'),
    localizationsDelegates: const [
      AppLocalizations.delegate,
      GlobalMaterialLocalizations.delegate,
      GlobalCupertinoLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
    ],
    supportedLocales: AppLocalizations.delegate.supportedLocales,
    builder: (context, child) {
      globalState.measure = Measure.of(context, 1);
      globalState.theme = CommonTheme.of(context, 1);
      return child!;
    },
    home: Scaffold(body: SingleChildScrollView(child: child)),
  );
}
