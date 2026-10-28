"""Writes the pickles that the tests of package pickle read.

Run it from the module root: python3 -I internal/pickle/testdata/gen.py
"""

import os
import pickle
import sys

sys.dont_write_bytecode = True

here = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, here)

import fakerenpy  # noqa: E402

for protocol in range(0, 6):
    with open(os.path.join(here, "sample-p%d.pickle" % protocol), "wb") as f:
        f.write(pickle.dumps(fakerenpy.sample(), protocol))

with open(os.path.join(here, "large-p5.pickle"), "wb") as f:
    f.write(pickle.dumps(fakerenpy.sample(large=True), 5))
