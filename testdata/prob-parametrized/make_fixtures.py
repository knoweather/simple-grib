"""Rebuild the 4.112 / 4.123 fixtures in this directory with ecCodes.

Requires the ecCodes Python bindings plus a samples directory:

    $env:ECCODES_SAMPLES_PATH = "<eccodes>/samples"
    python make_fixtures.py

Each fixture is a regular_ll_sfc GRIB2 message switched to template 4.112 or
4.123, with sentinel values throughout the probability block and NT = 1 / 3.
The script prints ecCodes' own decoded values and octet offsets so the
expectations in product_prob_parametrized_test.go stay traceable.
"""
import os

from eccodes import (
    codes_get, codes_get_offset, codes_grib_new_from_samples, codes_release,
    codes_set, codes_write,
)

HERE = os.path.dirname(os.path.abspath(__file__))

SENTINELS = [
    ("typeOfStatisticalProcessing", 1),
    ("typeOfTimeIncrement", 11),
    ("lengthOfTimeRange", 1111),
    ("indicatorOfUnitForTimeRange", 13),
    ("timeIncrement", 2222),
    ("numberOfForecastsInEnsemble", 3333),
    ("forecastProbabilityNumber", 17),
    ("totalNumberOfForecastProbabilities", 19),
    ("probabilityType", 5),
    ("scaleFactorOfLowerLimit", 2),
    ("scaledValueOfLowerLimit", 4444),
    ("scaleFactorOfUpperLimit", 3),
    ("scaledValueOfUpperLimit", 5555),
]

READ_BACK = [
    "numberOfTimeRanges", "typeOfStatisticalProcessing", "probabilityType",
    "scaleFactorOfLowerLimit", "scaledValueOfLowerLimit",
    "scaleFactorOfUpperLimit", "scaledValueOfUpperLimit",
]


def build(tmpl: int, nt: int) -> None:
    gid = codes_grib_new_from_samples("GRIB2")
    codes_set(gid, "productDefinitionTemplateNumber", tmpl)
    codes_set(gid, "numberOfTimeRanges", nt)
    for key, val in SENTINELS:
        try:
            codes_set(gid, key, val)
        except Exception:  # key absent on this template: fine
            pass

    path = os.path.join(HERE, f"p{tmpl}_nt{nt}.grib2")
    with open(path, "wb") as fh:
        codes_write(gid, fh)

    print(f"== template 4.{tmpl} NT={nt} -> {os.path.basename(path)}")
    for key in READ_BACK:
        try:
            print(f"   {key:32s} = {codes_get(gid, key)!r}"
                  f"  offset={codes_get_offset(gid, key)}")
        except Exception as exc:
            print(f"   {key:32s} ! {str(exc).splitlines()[0][:60]}")
    codes_release(gid)


if __name__ == "__main__":
    for template in (112, 123):
        for count in (1, 3):
            build(template, count)