document.addEventListener('alpine:init', () => {
    let pdfDoc;
    Alpine.data('app', () => ({
        auto: true,
        blank: false,
        npages: 0,
        currpage: 0,
        pageNum: 1,
        autoPageNum: 0,
        autoInfo: '-',
        pageRendering: false,
        scale: 1,
        forceScale: false,
        pageNumPending: null,
        filter: '',
        file: 0,
        socket: null,
        channels: [],
        status: 'Connecting...',
        pdfs: [],
        pdf: '',
        connected: false,
        universe: 0,
        channel: 1,
        init() {
            this.connect();
            if (!window.WebSocket) { return; }
            setInterval(() => { this.connect(); }, 3000);
        },
        setup() {
            this.socket.send(JSON.stringify({
                uni: this.universe,
                ch: this.channel,
                filter: this.filter,
                file: this.pdf,
            }));
        },
        setPDF(file) {
            if (file == "-") { return; }
            if (file == "Refresh...") {
                this.pdf = "-";
                this.socket.send('pdf');
                return;
            }
            var loadingTask = pdfjsLib.getDocument("pdf/" + file);
            loadingTask.promise.then((pdf) => {
                pdfDoc = pdf;
                this.npages = pdf.numPages;
                this.renderPage(1);
            });
        },
        connect() {
            if (this.connected) { return; }
            if (!window.WebSocket) {
                alert("This browser does not support the WebSocket API. This page will not be able to receive data.");
                this.status = 'Unsupported'
            } else {
                var s = ((window.location.protocol === "https:") ? "wss://" : "ws://") + window.location.host + "/ws";
                this.socket = new WebSocket(s);
                this.socket.onopen = () => {
                    this.status = 'Connected.'
                    this.connected = true;
                    if (this.socket.readyState) {
                        this.socket.send('pdf');
                        this.socket.send('set');
                    }
                }
                this.socket.onerror = (ev) => {
                    console.log(ev);
                }
                this.socket.onclose = () => {
                    this.status = 'Disconnected.';
                    this.connected = false;
                }
                this.socket.onmessage = (ev) => {
                    var msg;
                    try {
                        msg = JSON.parse(ev.data);
                    } catch (e) {
                        console.log(e, ev);
                        return;
                    }
                    // console.log(msg);
                    if ('uni' in msg) {
                        this.universe = msg.uni;
                        this.channel = msg.ch;
                        this.filter = msg.filter;
                        this.pdf = msg.file;
                        this.setPDFs();
                        return;
                    }
                    if ('ch' in msg) {
                        this.channels = msg.ch;
                        this.parse(msg.ch);
                        return;
                    }
                    if ('pdfs' in msg) {
                        this.setPDFs(msg.pdfs);                        
                        return;
                    }
                    console.log(msg);
                };
            }
        },
        setPDFs(pdfs) {
            if (pdfs) { this.pdfs = pdfs; }
            if (this.pdfs.length == 1) {
                if (this.pdfs[0] != this.pdf) {
                    this.pdf = this.pdfs[0];
                    this.load();
                }
            }
        },
        loadSet() {
            this.load();
            this.setup();
        },
        load() {
            this.setPDF(this.pdf);
        },
        parse(ch) {
            this.blank = ch[1] == 255;
            this.file = ch[2];
            var page = 0;
            if (ch[1] == 0) {
                page = Math.floor((ch[0] / 255) * this.npages) + 1;
                this.autoInfo = (ch[0] / 2.55).toFixed(1) + '%';
            } else {
                page = ch[0] + 1 + 256 * (ch[1] - 1);
                this.autoInfo = page;
            }
            if (page < 1) { page = 1; }
            if (page > this.npages ) { page = this.npages; }
            this.autoPageNum = page;
            if (this.blank) { this.autoInfo = 'blanked'; }
            if (!this.auto) { return; }
            if (this.pageNum == this.autoPageNum) { return; }
            this.pageNum = this.autoPageNum;
            this.queuePage();
        },
        prev() {
            this.auto = false;
            if (this.pageNum <= 1) { return; }
            this.pageNum--;
            this.queuePage();
        },
        next() {
            this.auto = false;
            if (this.pageNum >= pdfDoc.numPages) { return; }
            this.pageNum++;
            this.queuePage();
        },
        getStyle() {
            return {
                inverted: this.filter == "Inverted",
                huerotated: this.filter == "Sepia",
                hidden: (this.auto && this.blank) || (this.pdf == '-'),
            }
        },
        toggleAuto() {
            this.auto = !this.auto;
            if (this.auto && (this.autoPageNum != this.pageNum)) {
                this.pageNum = this.autoPageNum;
                this.queuePage();
            }
        },
        setScale() { this.forceScale = true; this.queuePage(); },
        resetScale() { this.forceScale = false; this.queuePage(); },
        queuePage() { if (this.pageRendering) { this.pageNumPending = this.pageNum; } else { this.renderPage(this.pageNum); } },
        renderPage(num) {
            this.pageRendering = true;
            // Using promise to fetch the page
            if (!pdfDoc) { return; }
            pdfDoc.getPage(num).then((page) => {
                canvas = document.getElementById('cnv');
                ctx = canvas.getContext('2d');

                var view = page.getViewport({ scale: 1.0 });
                var screenHeight = window.screen.height;
                if (!this.forceScale) { this.scale = screenHeight / view.height; }
                // console.log(view, canvas, this.scale, canvas.height, view.height);
                if (isNaN(this.scale)) { this.scale = 1.0; }
                var viewport = page.getViewport({ scale: this.scale });

                canvas.height = viewport.height;
                canvas.width = viewport.width;

                // Render PDF page into canvas context
                var renderContext = {
                    canvasContext: ctx,
                    viewport: viewport
                };
                var renderTask = page.render(renderContext);

                // Wait for rendering to finish
                renderTask.promise.then(() => {
                    this.pageRendering = false;
                    if (this.pageNumPending !== null) {
                        // New page rendering is pending
                        this.renderPage(this.pageNumPending);
                        this.pageNumPending = null;
                    }
                });
            });
            this.currpage = num;
        }
    }))
})