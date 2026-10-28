// load-tests/helpers/summary_reporter.js
// Exports aggregated test metrics and execution statistics to a formatted JSON report.

export function createSummaryHandler(reportFileName) {
  return function (data) {
    const reportsDir = __ENV.REPORTS_DIR || 'load-tests/reports';
    const reportPath = `${reportsDir}/${reportFileName}`;
    const output = {};

    // Persist full structured summary data into JSON file
    output[reportPath] = JSON.stringify(data, null, 2);

    return output;
  };
}
