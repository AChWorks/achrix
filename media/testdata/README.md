# Owned complete Media fixtures

All payloads in this directory are synthetic first-party fixtures (MPL-2.0), authored by `generate.py`. They contain only a solid 32×32 image, a 0.12-second color/sine signal, or the text “AChrix owned media fixture”. No upstream MIME-detector testdata, customer document, macro, third-party media or truncated happy-path prefix is copied.

Generation used task-local Pillow 12.2.0 (image formats), imageio-ffmpeg 0.6.0's FFmpeg 7.0.2 (audio/video), 7zip 25.01 (7Z/archive checks), and LibreOffice 25.2.3.2 (owned flat ODF → DOC/DOCX/XLS/XLSX/PPT/PPTX/ODT/ODS/ODP/PDF). ZIP and complete stored RAR4 headers/payload/CRCs/end marker are authored with Python's standard library. These are generation tools, not Media/consumer dependencies. Pillow/FFmpeg wheel RECORD hashes were verified. LibreOffice/7zip packages came from Debian 13 signed repository metadata verified with the installed Debian archive keyring and were only extracted into the task directory; nothing was installed globally and no service ran.

Generation validates every image by complete Pillow decoding, every audio/video stream by FFmpeg `-xerror` complete decoding, and each archive by 7zip `t`. Every owned legacy and packaged Office export is independently reopened in LibreOffice and exported to a nonempty PDF. Only our own authored documents are opened.

LibreOffice's normal legacy CFB exports put subtype directories beyond Media's 4096-byte recognition prefix. `directory-beyond-prefix.doc/xls/ppt` preserve those complete outputs as honest rejection evidence. The positive `sample.doc/xls/ppt` fixtures use a narrow test-only permutation of these owned no-DIFAT CFB sectors: directory/FAT first, with regular-sector pointers remapped and document streams untouched. The permuted files are reopened/exported by LibreOffice too. This provides genuine positive recognition evidence without adding a runtime parser or implying that all valid/typical Office layouts will be admitted.

The generator accepts explicit `PYTHONPATH`, `FFMPEG`, `SEVENZIP` and `SOFFICE` tool paths. Isolated Debian LibreOffice extraction also needs its normal configuration/library locations relocated within the extraction. Tools are optional for regenerating fixtures; ordinary Go tests read the committed complete files.

Primary tool sources: [Pillow](https://pillow.readthedocs.io/), [imageio-ffmpeg](https://github.com/imageio/imageio-ffmpeg), [FFmpeg](https://ffmpeg.org/), [Debian LibreOffice](https://packages.debian.org/trixie/libreoffice), [7zip](https://www.7-zip.org/).
